package dto

import "collaborative-blackboard/internal/domain"

//IncomingAction 表示从客户端 WebSocket 消息中接受的某个操作的数据结构

type IncomingAction struct {
	Type string      `json:"type" binding:"required,oneof=draw erase"`
	X    int         `json:"x"`
	Y    int         `json:"y"`
	Color string      `json:"color,omitempty"`
	BasedOnVersion uint `json:"basedOnVersion"`
}

//OutgoingAction 表示要发送到客户端的操作数据结构（目前是与domain.Action相同）


//SnapshiotDTO 表示需要时发送给客户端的快照数据结构

type SnapshiotDTO struct {
	Type string      `json:"type"`
	Version uint `json:"version"`
	State domain.BoardState `json:"state"`
}

//ErrorDTO 表示发送给客户端的错误消息数据结构

type ErrorDTO struct {
	Type string `json:"type"`
	Message string `json:"message"`
}