# Collaborative Blackboard API

## 1. Register
- **Method**: POST
- **URL**: /register
- **Request**: { "username": "string", "password": "string", "email": "string" }
- **Response**: 200 { "message": "User registered", "id": uint }

## 2. Login
- **Method**: POST
- **URL**: /login
- **Request**: { "username": "string", "password": "string" }
- **Response**: 200 { "message": "Login successful", "token": "string" }

## 3. Create Room
- **Method**: POST
- **URL**: /api/room
- **Header**: Authorization: Bearer <token>
- **Response**: 200 { "message": "Room created", "room_id": uint, "invite_code": "string" }

## 4. Join Room
- **Method**: POST
- **URL**: /api/room/join
- **Header**: Authorization: Bearer <token>
- **Request**: { "invite_code": "string" }
- **Response**: 200 { "message": "Joined room", "room_id": uint }

## 5. WebSocket
- **URL**: /ws/:roomId
- **Header**: Authorization: Bearer <token>
- **Message**: { "type": "ready" } 或 { "x": int, "y": int, "color": "string" }