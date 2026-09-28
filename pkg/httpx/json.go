package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// MaxBodyBytes is the largest request body Decode accepts.
const MaxBodyBytes = 1 << 20

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Report field paths using JSON names so clients can map them to form fields.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "-":
			return ""
		case "":
			return f.Name
		}
		return name
	})
	return v
}

// WriteJSON writes v as JSON with the given status. A nil v writes no body.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Decode reads a single JSON object from the request body into dst (unknown
// fields rejected, max MaxBodyBytes) and validates it with `validate` tags.
// Every failure is a 400 VALIDATION_ERROR *AppError.
func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return bodyError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ValidationError(map[string]string{"body": "must contain a single JSON object"})
	}
	return Validate(dst)
}

func bodyError(err error) *AppError {
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		return ValidationError(map[string]string{"body": fmt.Sprintf("must not exceed %d bytes", MaxBodyBytes)})
	case errors.Is(err, io.EOF):
		return ValidationError(map[string]string{"body": "is required"})
	default:
		return ValidationError(map[string]string{"body": "invalid JSON: " + err.Error()})
	}
}

// Validate runs struct validation and converts failures to a VALIDATION_ERROR.
func Validate(v any) error {
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return NewError(CodeInternal, "internal server error").Wrap(err)
	}
	fields := make(map[string]string, len(ve))
	for _, fe := range ve {
		fields[fieldPath(fe)] = fieldMessage(fe)
	}
	return ValidationError(fields)
}

// fieldPath drops the top-level struct name: "productIn.skus[0].name" → "skus[0].name".
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if _, rest, ok := strings.Cut(ns, "."); ok {
		return rest
	}
	return ns
}

func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "uuid", "uuid4", "uuid7":
		return "must be a valid UUID"
	case "min", "gte":
		return "must be at least " + fe.Param()
	case "max", "lte":
		return "must be at most " + fe.Param()
	case "len":
		return "must have length " + fe.Param()
	case "oneof":
		return "must be one of: " + fe.Param()
	default:
		return "failed '" + fe.Tag() + "' validation"
	}
}
