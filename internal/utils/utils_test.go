package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

func TestIsStrongPassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{"valid", "Secret123", true},
		{"too short", "Se1", false},
		{"no uppercase", "secret123", false},
		{"no lowercase", "SECRET123", false},
		{"no digit", "SecretSecret", false},
		{"longer than bcrypt limit", strings.Repeat("Aa1", 25), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsStrongPassword(tc.password))
		})
	}
}

func TestSlugify(t *testing.T) {
	assert.Equal(t, "sepatu-lari-pria", Slugify("  Sepatu Lari Pria! "))
	assert.Equal(t, "kopi-arabika-250g", Slugify("Kopi Arabika (250g)"))
	assert.Equal(t, "", Slugify("!!!"))
}

func TestNewPagination(t *testing.T) {
	assert.Equal(t, Pagination{Page: 1, Limit: 20}, NewPagination(0, 0))
	assert.Equal(t, Pagination{Page: 1, Limit: 20}, NewPagination(-5, -1))
	assert.Equal(t, Pagination{Page: 3, Limit: 100}, NewPagination(3, 5000))
	assert.Equal(t, 40, NewPagination(3, 20).Offset())
}

func TestValidateStruct_ReportsJSONFieldNames(t *testing.T) {
	type request struct {
		Email    string `json:"email" validate:"required,email"`
		Password string `json:"password" validate:"required,password"`
	}

	err := ValidateStruct(&request{Email: "not-an-email", Password: "weak"})

	var appErr *apperror.AppError
	if assert.ErrorAs(t, err, &appErr) {
		assert.Equal(t, apperror.CodeValidation, appErr.Code)
		fields := map[string]bool{}
		for _, d := range appErr.Details {
			fields[d.Field] = true
		}
		assert.True(t, fields["email"])
		assert.True(t, fields["password"])
	}

	assert.NoError(t, ValidateStruct(&request{Email: "a@b.co", Password: "Secret123"}))
}
