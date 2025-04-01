package handlers

import (
	"collaborative-blackboard/models"
	"log"
)

// Transform 转换两个并发操作
func Transform(op1, op2 models.Action) (models.Action, models.Action) {
	// 非 "draw" 或 "erase" 不转换
	if (op1.ActionType != "draw" && op1.ActionType != "erase") ||
		(op2.ActionType != "draw" && op2.ActionType != "erase") {
		return op1, op2
	}

	// 解析 Data
	data1, err1 := op1.ParseData()
	if err1 != nil {
		log.Printf("Failed to parse op1 data: %v", err1)
		return op1, op2
	}
	data2, err2 := op2.ParseData()
	if err2 != nil {
		log.Printf("Failed to parse op2 data: %v", err2)
		return op1, op2
	}

	// 检查坐标冲突
	if data1.X == data2.X && data1.Y == data2.Y {
		switch {
		case op1.ActionType == "draw" && op2.ActionType == "draw":
			// 两个画点冲突，后者覆盖前者
			if op1.Version > op2.Version || (op1.Version == op2.Version && op1.Timestamp.After(op2.Timestamp)) {
				nop := op2
				nop.ActionType = "noop"
				nop.Data = ""
				return op1, nop
			} else {
				nop := op1
				nop.ActionType = "noop"
				nop.Data = ""
				return nop, op2
			}
		case op1.ActionType == "erase" && op2.ActionType == "draw":
			// 擦除 vs 画点：擦除优先
			if op1.Version > op2.Version || (op1.Version == op2.Version && op1.Timestamp.After(op2.Timestamp)) {
				nop := op2
				nop.ActionType = "noop"
				nop.Data = ""
				return op1, nop
			} else {
				nop := op1
				nop.ActionType = "noop"
				nop.Data = ""
				return nop, op2
			}
		case op1.ActionType == "draw" && op2.ActionType == "erase":
			// 画点 vs 擦除：擦除优先
			if op2.Version > op1.Version || (op2.Version == op1.Version && op2.Timestamp.After(op1.Timestamp)) {
				nop := op1
				nop.ActionType = "noop"
				nop.Data = ""
				return nop, op2
			} else {
				nop := op2
				nop.ActionType = "noop"
				nop.Data = ""
				return op1, nop
			}
		case op1.ActionType == "erase" && op2.ActionType == "erase":
			// 两个擦除相同位置，无需重复
			if op1.Version > op2.Version {
				nop := op2
				nop.ActionType = "noop"
				nop.Data = ""
				return op1, nop
			} else {
				nop := op1
				nop.ActionType = "noop"
				nop.Data = ""
				return nop, op2
			}
		}
	}

	// 无冲突，返回原操作
	return op1, op2
}
