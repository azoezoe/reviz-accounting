package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/jackc/pgx/v5/stdlib"
	"strconv"
	"strings"
)

func init() { sql.Register("reviz-pgx", rebindDriver{base: stdlib.GetDefaultDriver()}) }

type rebindDriver struct{ base driver.Driver }

func (d rebindDriver) Open(name string) (driver.Conn, error) {
	c, e := d.base.Open(name)
	return rebindConn{Conn: c}, e
}

type rebindConn struct{ driver.Conn }

func (c rebindConn) Prepare(q string) (driver.Stmt, error) { return c.Conn.Prepare(rebind(q)) }
func (c rebindConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if x, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return x.PrepareContext(ctx, rebind(q))
	}
	return c.Prepare(q)
}
func (c rebindConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	if x, ok := c.Conn.(driver.ExecerContext); ok {
		return x.ExecContext(ctx, rebind(q), a)
	}
	return nil, driver.ErrSkip
}
func (c rebindConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	if x, ok := c.Conn.(driver.QueryerContext); ok {
		return x.QueryContext(ctx, rebind(q), a)
	}
	return nil, driver.ErrSkip
}
func (c rebindConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	if x, ok := c.Conn.(driver.ConnBeginTx); ok {
		return x.BeginTx(ctx, o)
	}
	return c.Conn.Begin()
}
func (c rebindConn) Ping(ctx context.Context) error {
	if x, ok := c.Conn.(driver.Pinger); ok {
		return x.Ping(ctx)
	}
	return nil
}
func rebind(q string) string {
	var b strings.Builder
	n := 0
	quote := byte(0)
	for i := 0; i < len(q); i++ {
		ch := q[i]
		if quote != 0 {
			b.WriteByte(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			b.WriteByte(ch)
			continue
		}
		if ch == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}
