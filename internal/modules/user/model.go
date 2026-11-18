// Package user manages user accounts and profiles.
package user

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User is the users table. It is never serialized directly: handlers always
// convert it to a DTO, so the password hash cannot leak into a response.
type User struct {
	ID           uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name         string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

// UpdateProfileRequest is the body of PUT /users/me.
type UpdateProfileRequest struct {
	Name string `json:"name" validate:"required,min=2,max=100"`
}

// ChangePasswordRequest is the body of PUT /users/me/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password" validate:"required,password,nefield=CurrentPassword"`
}

// Response is the public representation of a user.
type Response struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ToResponse maps the entity to its public DTO.
func ToResponse(u *User) Response {
	return Response{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// ToResponses maps a slice of entities.
func ToResponses(users []User) []Response {
	out := make([]Response, 0, len(users))
	for i := range users {
		out = append(out, ToResponse(&users[i]))
	}
	return out
}
