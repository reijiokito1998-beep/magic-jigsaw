package api

import (
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strings"

	"github.com/reijiokito/jigsaw-backend/internal/auth"
	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

const (
	minPasswordLen = 8
	// bcrypt only considers the first 72 bytes; also rejects absurd inputs.
	maxPasswordLen = 72

	// Email size limits, all from the SMTP/DNS specs rather than taste:
	// RFC 5321 caps a forward-path at 254 bytes and a local-part at 64;
	// RFC 1035 caps a DNS name at 253 bytes and one label at 63.
	maxEmailLen       = 254
	maxEmailLocalLen  = 64
	maxEmailDomainLen = 253
	maxEmailLabelLen  = 63
)

type registerRequest struct {
	Email           string `json:"email"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string      `json:"token"`
	User  models.User `json:"user"`
}

// handleRegister creates a new email/password account and returns a session.
//
// @Summary      Register
// @Description  Creates an account with email + password (password is bcrypt-hashed) and returns a session JWT.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body  registerRequest  true  "Registration"
// @Success      201  {object}  authResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Router       /api/v1/auth/register [post]
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleRegister: error decoding request body")
		return
	}

	email := normalizeEmail(req.Email)
	log.Printf("[DEBUG] handleRegister: start email=%s", email)
	if !isValidEmail(email) {
		log.Printf("[DEBUG] handleRegister: error invalid email email=%s", email)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "email không hợp lệ")
		return
	}
	if len(req.Password) < minPasswordLen {
		log.Printf("[DEBUG] handleRegister: error password too short email=%s", email)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "mật khẩu tối thiểu 8 ký tự")
		return
	}
	if len(req.Password) > maxPasswordLen {
		log.Printf("[DEBUG] handleRegister: error password too long email=%s", email)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "mật khẩu tối đa 72 ký tự")
		return
	}
	if req.Password != req.ConfirmPassword {
		log.Printf("[DEBUG] handleRegister: error password confirmation mismatch email=%s", email)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "mật khẩu xác nhận không khớp")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		log.Printf("[DEBUG] handleRegister: error hashing password email=%s: %v", email, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not hash password")
		return
	}

	role := models.RoleUser
	if s.cfg.IsAdminEmail(email) {
		role = models.RoleAdmin
		log.Printf("[DEBUG] handleRegister: email=%s matches admin allow-list, assigning admin role", email)
	}

	user, err := s.store.CreateUser(r.Context(), repository.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Name:         randomDisplayName(),
		Role:         role,
	})
	if err != nil {
		metrics.AuthEventsTotal.WithLabelValues("register", "failure").Inc()
		if errors.Is(err, repository.ErrEmailTaken) {
			log.Printf("[DEBUG] handleRegister: email already taken email=%s", email)
			httpx.Error(w, http.StatusConflict, "email_taken", "email đã được đăng ký")
			return
		}
		log.Printf("[DEBUG] handleRegister: error CreateUser email=%s: %v", email, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create account")
		return
	}

	metrics.AuthEventsTotal.WithLabelValues("register", "success").Inc()
	log.Printf("[DEBUG] handleRegister: success userID=%s email=%s name=%s role=%s", user.ID, email, user.Name, role)
	s.issueSession(w, user, http.StatusCreated)
}

// handleLogin authenticates an email/password account and returns a session.
//
// @Summary      Login
// @Description  Authenticates with email + password and returns a session JWT.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body  loginRequest  true  "Credentials"
// @Success      200  {object}  authResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/auth/login [post]
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleLogin: error decoding request body")
		return
	}
	email := normalizeEmail(req.Email)
	log.Printf("[DEBUG] handleLogin: start email=%s", email)
	if email == "" || req.Password == "" {
		log.Printf("[DEBUG] handleLogin: error missing email or password email=%s", email)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "email và mật khẩu là bắt buộc")
		return
	}
	// Reject malformed addresses before touching the database. No account can
	// exist under one — register enforces the same rule — so this leaks nothing
	// that the client could not already determine itself, and it keeps garbage
	// input off the credential-stuffing path.
	if !isValidEmail(email) {
		log.Printf("[DEBUG] handleLogin: error invalid email email=%s", email)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "email không hợp lệ")
		return
	}

	user, hash, err := s.store.GetUserAuthByEmail(r.Context(), email)
	if err != nil || !auth.CheckPassword(hash, req.Password) {
		// A sustained rise in this counter is the signal for credential
		// stuffing, well before the auth rate limiter starts rejecting.
		metrics.AuthEventsTotal.WithLabelValues("login", "failure").Inc()
		// Same message whether the email is unknown or the password is wrong.
		// Never log the raw password; log only the outcome.
		log.Printf("[DEBUG] handleLogin: invalid credentials email=%s", email)
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "email hoặc mật khẩu không đúng")
		return
	}

	// Keep the role in sync with the admin allow-list.
	desiredRole := models.RoleUser
	if s.cfg.IsAdminEmail(email) {
		desiredRole = models.RoleAdmin
	}
	if user.Role != desiredRole {
		if err := s.store.UpdateUserRole(r.Context(), user.ID, desiredRole); err == nil {
			log.Printf("[DEBUG] handleLogin: role synced userID=%s oldRole=%s newRole=%s", user.ID, user.Role, desiredRole)
			user.Role = desiredRole
		} else {
			log.Printf("[DEBUG] handleLogin: error UpdateUserRole userID=%s: %v", user.ID, err)
		}
	}

	metrics.AuthEventsTotal.WithLabelValues("login", "success").Inc()
	log.Printf("[DEBUG] handleLogin: success userID=%s email=%s", user.ID, email)
	s.issueSession(w, user, http.StatusOK)
}

func (s *Server) issueSession(w http.ResponseWriter, user models.User, status int) {
	token, err := s.jwt.Issue(user.ID, user.Role)
	if err != nil {
		log.Printf("[DEBUG] issueSession: error issuing token userID=%s: %v", user.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not issue token")
		return
	}
	// Never log the token value itself.
	log.Printf("[DEBUG] issueSession: issued session userID=%s status=%d", user.ID, status)
	httpx.JSON(w, status, authResponse{Token: token, User: user})
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// isValidEmail reports whether email is a plain, deliverable-looking address.
//
// Stricter than RFC 5322 on purpose. net/mail alone accepts forms that are
// legal in a mail header but wrong as an account identifier — display names
// ("Ai <a@b.c>"), quoted locals, bracketed IP domains, and bare hostnames with
// no dot. So the parse is only the first gate: the address must also come back
// byte-identical to the input, and the domain must look like a real dotted
// hostname.
//
// Expects an already-normalized (trimmed, lower-cased) address.
func isValidEmail(email string) bool {
	if email == "" || len(email) > maxEmailLen {
		return false
	}
	// A parsed address that differs from the input means the input carried
	// extra syntax (display name, angle brackets, quotes) — not an identifier.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}

	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	local, domain := email[:at], email[at+1:]
	return isValidEmailLocal(local) && isValidEmailDomain(domain)
}

func isValidEmailLocal(local string) bool {
	// Quoted locals ("odd name"@x.com) are legal but never what a signup form
	// means, and they smuggle spaces and '@' past naive downstream parsing.
	return local != "" && len(local) <= maxEmailLocalLen && !strings.ContainsAny(local, `" `)
}

// isValidEmailDomain requires a dotted hostname: labels of alphanumerics and
// inner hyphens, ending in an alphabetic TLD. Rejects "a@b", "a@b.", "a@-b.c"
// and "a@[192.168.0.1]" — all of which mail.ParseAddress happily accepts.
func isValidEmailDomain(domain string) bool {
	if domain == "" || len(domain) > maxEmailDomainLen {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > maxEmailLabelLen {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
			if !isAlnum && c != '-' {
				return false
			}
		}
	}
	// TLD: letters only, at least two of them.
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for i := 0; i < len(tld); i++ {
		if tld[i] < 'a' || tld[i] > 'z' {
			return false
		}
	}
	return true
}
