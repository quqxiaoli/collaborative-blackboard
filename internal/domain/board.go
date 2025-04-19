package domain

import (
)

// BoardState 定义了画板状态的数据结构。
// 使用 map 将坐标（格式化为 "x:y" 字符串）映射到颜色字符串。
type BoardState map[string]string // 例如: {"10:20": "#FF0000", "11:21": "#0000FF"}

