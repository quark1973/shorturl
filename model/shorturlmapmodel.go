package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ ShortUrlMapModel = (*customShortUrlMapModel)(nil)

type (
	// ShortUrlMapModel is an interface to be customized, add more methods here,
	// and implement the added methods in customShortUrlMapModel.
	ShortUrlMapModel interface {
		shortUrlMapModel
		ListAllSurl(ctx context.Context) ([]string, error)
	}

	customShortUrlMapModel struct {
		*defaultShortUrlMapModel
	}
)

// NewShortUrlMapModel returns a model for the database table.
func NewShortUrlMapModel(conn sqlx.SqlConn) ShortUrlMapModel {
	return &customShortUrlMapModel{
		defaultShortUrlMapModel: newShortUrlMapModel(conn),
	}
}

func (m *customShortUrlMapModel) withSession(session sqlx.Session) ShortUrlMapModel {
	return NewShortUrlMapModel(sqlx.NewSqlConnFromSession(session))
}

func (m *customShortUrlMapModel) ListAllSurl(ctx context.Context) ([]string, error) {
	var rows []struct {
		Surl string `db:"surl"`
	}
	query := fmt.Sprintf("select `surl` from %s where `is_del` = 0 and `surl` is not null", m.table)
	if err := m.conn.QueryRowsCtx(ctx, &rows, query); err != nil {
		return nil, err
	}

	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		codes = append(codes, row.Surl)
	}

	return codes, nil
}
