package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/bakhod1r/errorx"
	"go.uber.org/zap"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/relation"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/importer"
)

// Application error codes (errorx leaves 6xxx to applications). A code and
// its number never change once released; add new ones at the end.
const (
	ErrInvalidCredentials     = "INVALID_CREDENTIALS"
	ErrEmailNotVerified       = "EMAIL_NOT_VERIFIED"
	ErrUserBlocked            = "USER_BLOCKED"
	ErrImportForbiddenAddress = "IMPORT_FORBIDDEN_ADDRESS"
)

func init() {
	errorx.MustRegisterErrors(
		errorx.CustomError{
			Code: ErrInvalidCredentials, Numeric: "6001", HTTPStatus: http.StatusUnauthorized,
			Category: errorx.CategorySecurity, Layer: errorx.LayerHandler,
			Message: errorx.UserMessage{
				En: "Wrong login or password.",
				Uz: "Login yoki parol noto'g'ri.",
				Ru: "Неверный логин или пароль.",
			},
		},
		errorx.CustomError{
			Code: ErrEmailNotVerified, Numeric: "6002", HTTPStatus: http.StatusForbidden,
			Category: errorx.CategorySecurity, Layer: errorx.LayerDomain,
			Message: errorx.UserMessage{
				En: "Confirm your email address first.",
				Uz: "Avval email manzilingizni tasdiqlang.",
				Ru: "Сначала подтвердите адрес электронной почты.",
			},
		},
		errorx.CustomError{
			Code: ErrUserBlocked, Numeric: "6003", HTTPStatus: http.StatusBadRequest,
			Category: errorx.CategoryBusiness, Layer: errorx.LayerDomain,
			Message: errorx.UserMessage{
				En: "You cannot interact with this user.",
				Uz: "Bu foydalanuvchi bilan muloqot qila olmaysiz.",
				Ru: "Вы не можете взаимодействовать с этим пользователем.",
			},
		},
		errorx.CustomError{
			Code: ErrImportForbiddenAddress, Numeric: "6004", HTTPStatus: http.StatusBadRequest,
			Category: errorx.CategorySecurity, Layer: errorx.LayerHandler,
			Message: errorx.UserMessage{
				En: "This address cannot be imported.",
				Uz: "Bu manzildan import qilib bo'lmaydi.",
				Ru: "С этого адреса нельзя импортировать.",
			},
		},
	)
}

// toProblem maps an application error to the errorx error the client sees.
// detail is client-safe text: domain messages are written for users;
// anything unexpected becomes an opaque INTERNAL_ERROR and is logged.
func toProblem(err error, notFound string) *errorx.AppError {
	var invalid *domain.ValidationError
	switch {
	case errors.Is(err, importer.ErrForbiddenAddress):
		return errorx.New(ErrImportForbiddenAddress, "import").WithDetails(err.Error())
	case errors.Is(err, relation.ErrBlocked):
		return errorx.New(ErrUserBlocked, "blocked").WithDetails(err.Error())
	case errors.As(err, &invalid):
		return errorx.New(errorx.ErrValidation, "validation").WithDetails(invalid.Msg)
	case errors.Is(err, domain.ErrNotFound):
		if notFound == "" {
			notFound = "not found"
		}
		return errorx.New(errorx.ErrNotFound, "not found").WithDetails(notFound)
	case errors.Is(err, domain.ErrConflict):
		return errorx.New(errorx.ErrConflict, "conflict").WithDetails("already exists")
	case errors.Is(err, application.ErrInvalidCredentials):
		return errorx.New(ErrInvalidCredentials, "login").WithDetails(err.Error())
	case errors.Is(err, domain.ErrUnauthorized):
		return errorx.New(errorx.ErrUnauthorized, "unauthorized").WithDetails("unauthorized")
	case errors.Is(err, domain.ErrEmailNotVerified):
		return errorx.New(ErrEmailNotVerified, "email").WithDetails("email not verified")
	case errors.Is(err, domain.ErrForbidden):
		return errorx.New(errorx.ErrForbidden, "forbidden").WithDetails("forbidden")
	}
	zap.L().Error("request failed", zap.Error(err))
	return errorx.Wrap(err, errorx.ErrInternal, "unexpected")
}

// problem writes an errorx error as RFC 9457 problem details.
func problem(w http.ResponseWriter, r *http.Request, e *errorx.AppError) {
	if err := errorx.WriteProblemRequest(w, r, e); err != nil {
		zap.L().Error("write problem", zap.Any("ctx", r.Context()), zap.Error(err))
	}
}

// writeError answers with a request-level failure: status picks the code,
// msg is the client-facing detail.
func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	code := errorx.ErrInternal
	switch status {
	case http.StatusBadRequest:
		code = errorx.ErrBadRequest
	case http.StatusUnauthorized:
		code = errorx.ErrUnauthorized
	case http.StatusForbidden:
		code = errorx.ErrForbidden
	case http.StatusNotFound:
		code = errorx.ErrNotFound
	case http.StatusConflict:
		code = errorx.ErrConflict
	case http.StatusTooManyRequests:
		code = errorx.ErrHandlerTooManyRequests
	case http.StatusServiceUnavailable:
		code = errorx.ErrHandlerServiceUnavailable
	}
	problem(w, r, errorx.New(code, msg).WithDetails(msg))
}

// tooManyRequests is the 429 problem with the limiter's wait time.
func tooManyRequests(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	problem(w, r, errorx.New(errorx.ErrHandlerTooManyRequests, "rate limit").
		WithDetails("too many requests").WithRetryAfter(max(time.Second, retry)))
}
