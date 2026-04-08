package sqlstore

import "fmt"

type Dialect interface {
	Placeholder(n int) string
	AutoIncrement() string
	TimestampDefault() string
	JSONType() string
	Upsert(table, conflict, setCols string) string
}

type sqliteDialect struct{}

func (sqliteDialect) Placeholder(n int) string { return "?" }
func (sqliteDialect) AutoIncrement() string     { return "INTEGER PRIMARY KEY AUTOINCREMENT" }
func (sqliteDialect) TimestampDefault() string  { return "DATETIME DEFAULT CURRENT_TIMESTAMP" }
func (sqliteDialect) JSONType() string           { return "TEXT" }
func (sqliteDialect) Upsert(table, conflict, setCols string) string {
	return fmt.Sprintf("INSERT INTO %s %%s VALUES %%s ON CONFLICT(%s) DO UPDATE SET %s", table, conflict, setCols)
}

type postgresDialect struct{}

func (postgresDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }
func (postgresDialect) AutoIncrement() string     { return "BIGSERIAL PRIMARY KEY" }
func (postgresDialect) TimestampDefault() string  { return "TIMESTAMPTZ DEFAULT NOW()" }
func (postgresDialect) JSONType() string           { return "JSONB" }
func (postgresDialect) Upsert(table, conflict, setCols string) string {
	return fmt.Sprintf("INSERT INTO %s %%s VALUES %%s ON CONFLICT(%s) DO UPDATE SET %s", table, conflict, setCols)
}
