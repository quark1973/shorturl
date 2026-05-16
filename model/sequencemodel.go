package model

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ SequenceModel = (*customSequenceModel)(nil)

type (
	// SequenceModel is an interface to be customized, add more methods here,
	// and implement the added methods in customSequenceModel.
	SequenceModel interface {
		sequenceModel
		AllocSegment(ctx context.Context, bizTag string, step uint64) (uint64, uint64, error)
	}

	customSequenceModel struct {
		*defaultSequenceModel
	}
)

// NewSequenceModel returns a model for the database table.
func NewSequenceModel(conn sqlx.SqlConn) SequenceModel {
	return &customSequenceModel{
		defaultSequenceModel: newSequenceModel(conn),
	}
}

func (m *customSequenceModel) withSession(session sqlx.Session) SequenceModel {
	return NewSequenceModel(sqlx.NewSqlConnFromSession(session))
}

func (m *customSequenceModel) AllocSegment(ctx context.Context, bizTag string, step uint64) (uint64, uint64, error) {
	if bizTag == "" {
		return 0, 0, fmt.Errorf("biz tag is empty")
	}

	var start, end uint64
	err := m.conn.TransactCtx(ctx, func(ctx context.Context, session sqlx.Session) error {
		var row struct {
			MaxId uint64 `db:"max_id"`
			Step  uint64 `db:"step"`
		}
		query := fmt.Sprintf("select `max_id`, `step` from %s where `biz_tag` = ? for update", m.table)
		if err := session.QueryRowCtx(ctx, &row, query, bizTag); err != nil {
			return err
		}

		segmentStep := step
		if segmentStep == 0 {
			segmentStep = row.Step
		}
		if segmentStep == 0 {
			return fmt.Errorf("sequence step is zero")
		}

		start = row.MaxId + 1
		end = row.MaxId + segmentStep
		query = fmt.Sprintf("update %s set `max_id` = ? where `biz_tag` = ?", m.table)
		_, err := session.ExecCtx(ctx, query, end, bizTag)
		return err
	})
	if err != nil {
		return 0, 0, err
	}

	return start, end, nil
}
