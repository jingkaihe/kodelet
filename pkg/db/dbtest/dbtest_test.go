package dbtest

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestVerifyConfigurationAcceptsOpenedDatabase(t *testing.T) {
	require.NoError(t, VerifyConfiguration(t.Context(), OpenTemp(t)))
}

func TestVerifyConfigurationReportsMismatches(t *testing.T) {
	for _, test := range []struct {
		name         string
		misconfigure func(*testing.T, *db.DB)
		want         string
	}{
		{
			name: "foreign keys off",
			misconfigure: func(t *testing.T, database *db.DB) {
				_, err := database.Writer.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`)
				require.NoError(t, err)
			},
			want: "expected writer foreign_keys 1",
		},
		{
			name: "full synchronous mode",
			misconfigure: func(t *testing.T, database *db.DB) {
				_, err := database.Writer.ExecContext(t.Context(), `PRAGMA synchronous=FULL`)
				require.NoError(t, err)
			},
			want: "expected writer synchronous 1",
		},
		{
			// The pool hands out the most recently returned connection first,
			// so a check of one connection would miss this writable reader.
			name: "writable reader behind others",
			misconfigure: func(t *testing.T, database *db.DB) {
				connections := make([]*sqlx.Conn, database.Reader.Stats().MaxOpenConnections)
				for i := range connections {
					connection, err := database.Reader.Connx(t.Context())
					require.NoError(t, err)
					connections[i] = connection
				}
				_, err := connections[0].ExecContext(t.Context(), `PRAGMA query_only=OFF`)
				require.NoError(t, err)
				for _, connection := range connections {
					require.NoError(t, connection.Close())
				}
			},
			want: "expected read-only readers",
		},
		{
			name: "closed database",
			misconfigure: func(t *testing.T, database *db.DB) {
				require.NoError(t, database.Close())
			},
			want: "failed to query the writer's journal_mode",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := OpenTemp(t)
			test.misconfigure(t, database)
			require.ErrorContains(t, VerifyConfiguration(t.Context(), database), test.want)
		})
	}
}
