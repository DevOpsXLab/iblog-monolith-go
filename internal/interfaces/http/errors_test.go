package http

import (
	"errors"
	"fmt"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/relation"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/importer"
	"github.com/bakhod1r/errorx"
)

func TestToProblem(t *testing.T) {
	for _, c := range []struct {
		err     error
		code    string
		numeric int
		status  int
		detail  string
	}{
		{domain.Invalid("title required"), errorx.ErrValidation, 1003, 400, "title required"},
		{fmt.Errorf("get: %w", domain.ErrNotFound), errorx.ErrNotFound, 1012, 404, "post not found"},
		{domain.ErrConflict, errorx.ErrConflict, 1015, 409, "already exists"},
		{domain.ErrUnauthorized, errorx.ErrUnauthorized, 1004, 401, "unauthorized"},
		{domain.ErrForbidden, errorx.ErrForbidden, 1008, 403, "forbidden"},
		{application.ErrInvalidCredentials, ErrInvalidCredentials, 6001, 401, application.ErrInvalidCredentials.Error()},
		{domain.ErrEmailNotVerified, ErrEmailNotVerified, 6002, 403, "email not verified"},
		{relation.ErrBlocked, ErrUserBlocked, 6003, 400, relation.ErrBlocked.Error()},
		{importer.ErrForbiddenAddress, ErrImportForbiddenAddress, 6004, 400, importer.ErrForbiddenAddress.Error()},
		{errors.New("pq: connection refused on 10.0.0.7"), errorx.ErrInternal, 1017, 500, ""},
	} {
		p := toProblem(c.err, "post not found").Problem("/x")
		if p.Code != c.code || p.NumericCode != c.numeric || p.Status != c.status || p.Detail != c.detail {
			t.Errorf("%v → %+v", c.err, p)
		}
	}
}
