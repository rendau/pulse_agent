package common

import (
	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Base struct {
	Con *pgxpool.Pool
	QB  squirrel.StatementBuilderType
}

func NewBase(con *pgxpool.Pool) *Base {
	return &Base{
		Con: con,
		QB:  squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}
}
