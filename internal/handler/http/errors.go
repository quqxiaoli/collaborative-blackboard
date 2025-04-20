package http

import (
	"collaborative-blackboard/internal/service"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func HandleServiceError(c *gin.Context, err error) {
    if errors.Is(err, service.ErrAuthenticationFailed) {
        ErrorResponse(c, http.StatusUnauthorized, err.Error())
    } else if errors.Is(err, service.ErrRegistrationFailed) {
        ErrorResponse(c, http.StatusBadRequest, err.Error())
    } else if errors.Is(err, service.ErrInvalidInviteCode) || errors.Is(err, service.ErrRoomNotFound) {
        ErrorResponse(c, http.StatusNotFound, err.Error())
    } else if errors.Is(err, service.ErrInvalidAction) {
        ErrorResponse(c, http.StatusBadRequest, err.Error())
    } else {
        // Log the internal error for debugging
        logrus.WithError(err).Error("Unhandled internal server error")
        ErrorResponse(c, http.StatusInternalServerError, "An unexpected error occurred")
    }
}