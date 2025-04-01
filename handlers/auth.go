package handlers

import (
    "collaborative-blackboard/config"
    "collaborative-blackboard/models"
    "fmt"
    "net/http"
    "os"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/golang-jwt/jwt/v4"
    "github.com/sirupsen/logrus"
    "golang.org/x/crypto/bcrypt"
)

func Register(c *gin.Context) {
    var user models.User
    if err := c.ShouldBindJSON(&user); err != nil {
        logrus.WithError(err).Warn("Invalid input in Register")
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
        return
    }

    hashedPassword, err := hashPassword(user.Password)
    if err != nil {
        logrus.WithError(err).Error("Failed to hash password")
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error"})
        return
    }
    user.Password = hashedPassword

    if err := config.DB.Create(&user).Error; err != nil {
        logrus.WithError(err).Warn("User creation failed")
        c.JSON(http.StatusBadRequest, gin.H{"error": "Username or email already exists"})
        return
    }
    logrus.WithField("user_id", user.ID).Info("User registered")
    c.JSON(http.StatusOK, gin.H{"message": "User registered", "id": user.ID})
}

func Login(c *gin.Context) {
    var input struct {
        Username string `json:"username"`
        Password string `json:"password"`
    }
    if err := c.ShouldBindJSON(&input); err != nil {
        logrus.WithError(err).Warn("Invalid input in Login")
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
        return
    }

    user, err := findUserByUsername(input.Username)
    if err != nil || !checkPassword(input.Password, user.Password) {
        logrus.WithField("username", input.Username).Warn("Invalid login attempt")
        c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
        return
    }

    token, err := generateJWT(user.ID)
    if err != nil {
        logrus.WithError(err).Error("Failed to generate token")
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error"})
        return
    }
    logrus.WithField("user_id", user.ID).Info("Login successful")
    c.JSON(http.StatusOK, gin.H{"message": "Login successful", "token": token})
}

func hashPassword(password string) (string, error) {
    bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    return string(bytes), err
}

func checkPassword(password, hash string) bool {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func generateJWT(userID uint) (string, error) {
    secret := os.Getenv("JWT_SECRET")
    if secret == "" {
        return "", fmt.Errorf("JWT_SECRET environment variable not set")
    }
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
        "user_id": userID,
        "exp":     time.Now().Add(24 * time.Hour).Unix(),
    })
    return token.SignedString([]byte(secret))
}

func findUserByUsername(username string) (models.User, error) {
    var user models.User
    err := config.DB.Where("username = ?", username).First(&user).Error
    return user, err
}