# Các loại mảnh ghép (Piece Types)

Tài liệu chi tiết về 9 loại mảnh ghép chuẩn trong game xếp hình.

---

## 📐 Hệ thống cạnh (Edge System)

Mỗi mảnh có 4 cạnh (top, right, bottom, left). Cạnh được phân loại:
- **`F` (Flat):** Cạnh ngoài, thẳng (biên giới của hình ảnh)
- **`T` (Tab):** Cạnh lồi (mũi nhô ra)
- **`B` (Blank):** Cạnh lõm (lõm vào)

**Quy tắc khớp:**
- `T` (tab) của mảnh A phải khớp với `B` (blank) của mảnh B kế cạnh.
- `F` chỉ có trên biên ngoài (4 cạnh của lưới toàn bộ).

---

## 9 Loại mảnh chuẩn

### **Loại 1: Góc trên-trái (Top-Left Corner)**
```
┌─────────┐
│  T1     │ Top: F (Flat)
├─────────┤ Right: T (Tab) hoặc B (Blank)
│         │ Bottom: T (Tab) hoặc B (Blank)
│         │ Left: F (Flat)
└─────────┘

Cấu hình cạnh: F-T-T-F hoặc F-B-B-F hoặc F-T-B-F hoặc F-B-T-F
Vị trí: Góc trên-trái của lưới (row=0, col=0)
Cạnh cố định: top=F, left=F
```

**Ví dụ:**
```
  ▄▄▄
 ▄   ▄
▐ T1  ▌▄
│     ▄▄
└─────┘
```

---

### **Loại 2: Cạnh trên (Top Edge)**
```
┌──────────────┐
│     T2       │ Top: F (Flat)
├──────────────┤ Right: T hoặc B
│              │ Bottom: T hoặc B
│              │ Left: T hoặc B
└──────────────┘

Cấu hình cạnh: F-T-T-T, F-T-B-B, F-B-T-B, v.v.
Vị trí: Hàng trên, giữa (row=0, col=1..n-2)
Cạnh cố định: top=F
```

**Ví dụ:**
```
  ▄▄▄▄▄
 ▄     ▄
▐▌ T2  ▌▄
 ▐    ▄▄
  └────┘
```

---

### **Loại 3: Góc trên-phải (Top-Right Corner)**
```
┌─────────┐
│   T3    │ Top: F (Flat)
├─────────┤ Right: F (Flat)
│         │ Bottom: T hoặc B
│         │ Left: T hoặc B
└─────────┘

Cấu hình cạnh: F-F-T-T, F-F-B-B, F-F-T-B, F-F-B-T
Vị trí: Góc trên-phải (row=0, col=n-1)
Cạnh cố định: top=F, right=F
```

**Ví dụ:**
```
    ▄▄▄
   ▄   ▄
  ▐ T3 ▌▐
   ▐  ▄▄
    └─┘
```

---

### **Loại 4: Cạnh trái (Left Edge)**
```
┌───────────┐
│    T4     │ Top: T hoặc B
├───────────┤ Right: T hoặc B
│           │ Bottom: T hoặc B
│           │ Left: F (Flat)
└───────────┘

Cấu hình cạnh: T-T-T-F, B-T-B-F, T-B-T-F, v.v.
Vị trí: Cột trái, giữa (row=1..n-2, col=0)
Cạnh cố định: left=F
```

**Ví dụ:**
```
 ▄▄▄
▄   ▄
 T4 ▌▄
▐  ▄▄
 ▄▄
```

---

### **Loại 5: Mảnh giữa (Center)**
```
┌──────────────┐
│      T5      │ Top: T hoặc B
├──────────────┤ Right: T hoặc B
│              │ Bottom: T hoặc B
│              │ Left: T hoặc B
└──────────────┘

Cấu hình cạnh: T-T-T-T, T-B-B-T, B-T-T-B, v.v.
Vị trí: Hàng giữa, cột giữa (row=1..n-2, col=1..n-2)
Cạnh cố định: không có (tất cả đều có thể T hoặc B)
Số lượng: (n-2) × (n-2) chiếc trong lưới n×n
```

**Ví dụ:**
```
  ▄▄▄
 ▄   ▄
▐ T5 ▌▄
 ▐  ▄▄
  ▄▄
```

---

### **Loại 6: Cạnh phải (Right Edge)**
```
┌───────────┐
│    T6     │ Top: T hoặc B
├───────────┤ Right: F (Flat)
│           │ Bottom: T hoặc B
│           │ Left: T hoặc B
└───────────┘

Cấu hình cạnh: T-F-T-T, B-F-B-B, T-F-B-T, v.v.
Vị trí: Cột phải, giữa (row=1..n-2, col=n-1)
Cạnh cố định: right=F
```

**Ví dụ:**
```
 ▄▄▄
▄   ▄
 T6 ▌▐
▐  ▄▄
 ▄▄
```

---

### **Loại 7: Góc dưới-trái (Bottom-Left Corner)**
```
┌─────────┐
│   T7    │ Top: T hoặc B
├─────────┤ Right: T hoặc B
│         │ Bottom: F (Flat)
│         │ Left: F (Flat)
└─────────┘

Cấu hình cạnh: T-T-F-F, B-B-F-F, T-B-F-F, B-T-F-F
Vị trí: Góc dưới-trái (row=n-1, col=0)
Cạnh cố định: bottom=F, left=F
```

**Ví dụ:**
```
 ▄▄▄
▄   ▄
 T7 ▌▄
│   ▄▄
└─────┘
```

---

### **Loại 8: Cạnh dưới (Bottom Edge)**
```
┌──────────────┐
│      T8      │ Top: T hoặc B
├──────────────┤ Right: T hoặc B
│              │ Bottom: F (Flat)
│              │ Left: T hoặc B
└──────────────┘

Cấu hình cạnh: T-T-F-T, B-B-F-B, T-B-F-T, v.v.
Vị trí: Hàng dưới, giữa (row=n-1, col=1..n-2)
Cạnh cố định: bottom=F
```

**Ví dụ:**
```
  ▄▄▄▄▄
 ▄     ▄
▐▌ T8  ▌▄
 │    ▄▄
  └────┘
```

---

### **Loại 9: Góc dưới-phải (Bottom-Right Corner)**
```
┌─────────┐
│   T9    │ Top: T hoặc B
├─────────┤ Right: F (Flat)
│         │ Bottom: F (Flat)
│         │ Left: T hoặc B
└─────────┘

Cấu hình cạnh: T-F-F-T, B-F-F-B, T-F-F-B, B-F-F-T
Vị trí: Góc dưới-phải (row=n-1, col=n-1)
Cạnh cố định: bottom=F, right=F
```

**Ví dụ:**
```
    ▄▄▄
   ▄   ▄
  ▐ T9 ▌▐
   │  ▄▄
    └─┘
```

---

## 📊 Bảng tóm tắt 9 loại

| Loại | Tên | Vị trí | Cạnh cố định | Số lượng | Cạnh thay đổi |
|------|-----|--------|-------------|---------|--------------|
| 1 | Top-Left Corner | (0,0) | F-?-?-F | 1 | right, bottom |
| 2 | Top Edge | (0, 1..n-2) | F-?-?-? | n-2 | right, bottom, left |
| 3 | Top-Right Corner | (0, n-1) | F-F-?-? | 1 | bottom, left |
| 4 | Left Edge | (1..n-2, 0) | ?-?-?-F | n-2 | top, right, bottom |
| 5 | Center | (1..n-2, 1..n-2) | ?-?-?-? | (n-2)² | top, right, bottom, left |
| 6 | Right Edge | (1..n-2, n-1) | ?-F-?-? | n-2 | top, bottom, left |
| 7 | Bottom-Left Corner | (n-1, 0) | ?-?-F-F | 1 | top, right |
| 8 | Bottom Edge | (n-1, 1..n-2) | ?-?-F-? | n-2 | top, right, left |
| 9 | Bottom-Right Corner | (n-1, n-1) | ?-F-F-? | 1 | top, left |

**Ký hiệu:** `F` = Flat, `T` = Tab, `B` = Blank, `?` = thay đổi (T hoặc B)

---

## 🔢 Công thức đếm

Cho lưới **n × n**:
- **4 góc:** 1 × 4 = **4 chiếc**
- **4 cạnh (không góc):** (n-2) × 4 = **4(n-2) chiếc**
- **Trung tâm:** (n-2)² = **(n-2)² chiếc**
- **Tổng cộng:** 4 + 4(n-2) + (n-2)² = **n² chiếc** ✓

**Ví dụ 4×4:**
- Góc: 4
- Cạnh: 4 × 2 = 8
- Tâm: 2 × 2 = 4
- **Tổng: 16 ✓**

---

## 🎨 Cấu hình cạnh chi tiết

### **Số lượng cấu hình T-B khác nhau**

Với mỗi loại mảnh, các cạnh thay đổi có thể là T hoặc B:

| Loại | Cạnh thay đổi | Số cấu hình | Ví dụ |
|------|--------------|-----------|-------|
| 1, 3, 7, 9 | 2 | 2² = 4 | T-T, T-B, B-T, B-B |
| 2, 4, 6, 8 | 3 | 2³ = 8 | T-T-T, T-T-B, ..., B-B-B |
| 5 | 4 | 2⁴ = 16 | T-T-T-T, T-T-T-B, ..., B-B-B-B |

---

## 💾 Cấu trúc dữ liệu (JSON)

```json
{
  "pieceType": "top-left-corner",
  "position": {
    "row": 0,
    "col": 0
  },
  "edges": {
    "top": "flat",
    "right": "tab",
    "bottom": "blank",
    "left": "flat"
  },
  "srcRect": {
    "x": 0,
    "y": 0,
    "w": 200,
    "h": 200
  },
  "outline": "Path(...)",
  "currentRotation": 0,
  "state": "inTray"
}
```

---

## 🔄 Xoay (Rotation) — Thay đổi cạnh

Khi mảnh xoay 90° theo chiều kim đồng hồ, cạnh dịch vòng:
- **Trước xoay:** (top, right, bottom, left) = (T, B, T, F)
- **Sau xoay 90°:** (T, T, B, T) ← left → top, top → right, v.v.

```javascript
rotateEdges(edges, rotation) {
  const rotationCount = rotation / 90;
  const edgesArray = [edges.top, edges.right, edges.bottom, edges.left];
  const rotated = edgesArray.slice(-rotationCount).concat(
    edgesArray.slice(0, -rotationCount)
  );
  return {
    top: rotated[0],
    right: rotated[1],
    bottom: rotated[2],
    left: rotated[3]
  };
}
```

---

## ✅ Quy trình sinh mảnh

1. **Xác định loại** dựa vào `(row, col)` → loại 1-9
2. **Gán cạnh cố định** (F) nếu có
3. **Sinh ngẫu nhiên cạnh thay đổi** (T/B) nhưng đảm bảo **khớp với lân cận**
   - `piece[row][col].right` = `piece[row][col+1].left` (bù nhau)
4. **Tạo Path/Outline** dựa vào cấu hình cạnh + kiểu Bézier
5. **Lưu `edges`** để check khớp lúc chơi

---

## 📝 Ghi chú

- **Cái răng lược (Jigsaw teeth)** được sinh bằng Bézier curve ~ 10-15% kích thước mảnh.
- **Khớp cạnh** là điều kiện bắt buộc để ghép, kiểm tra khi bắt dính (snapping).
- **Xoay** chỉ thay đổi `currentRotation`; `edges` không thay (giữ nguyên gốc) nhưng check khớp phải xét rotation.
- **Hiệu ứng**  (bộ lọc hay tô màu) có thể tùy chọn để dễ phân biệt các loại (nếu muốn).
