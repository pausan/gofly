//go:build e2e

// Copyright (C) 2026 Pau Sanchez
//
// MySQL comment parsing regressions, checked against real Flyway and SQL results.
package e2e

import (
	"testing"

	"github.com/pausan/gofly/lib"
)

// -----------------------------------------------------------------------------
// TestCompatMysqlComments
//
// History alone cannot catch silently dropped executable comments: both tools
// can record success while running different SQL. Check the resulting data too.
// -----------------------------------------------------------------------------
func TestCompatMysqlComments(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		if target.Dialect != lib.DialectMysql {
			t.Skip("MySQL comment syntax")
		}
		cases := []struct {
			name string
			sql  string
			want string
		}{
			{
				"executable_comments",
				`-- mysqldump-style statements and a version guard
/*!40101 CREATE TABLE e2e_users(id INT PRIMARY KEY, name VARCHAR(50) NOT NULL) */;
/*!40101 SET @comment_value = 'executed; comment' */;
/*!99999 SET @comment_value = 'wrong version' */;
/*!40101 INSERT INTO e2e_users VALUES(1, @comment_value) */;
/* ordinary comment; */;`,
				"executed; comment",
			},
			{
				"dump_foreign_key_checks",
				`CREATE TABLE e2e_users(id INT PRIMARY KEY, name VARCHAR(50) NOT NULL,
parent_id INT, FOREIGN KEY(parent_id) REFERENCES e2e_users(id)) ENGINE=InnoDB;
/*!40014 SET @saved_foreign_key_checks = @@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS = 0 */;
INSERT INTO e2e_users VALUES(1, 'foreign keys disabled', 99);
/*!40014 SET FOREIGN_KEY_CHECKS = @saved_foreign_key_checks */;`,
				"foreign keys disabled",
			},
			{
				"inline_executable_comments",
				`/*!40101 CREATE */ /*!40101 TABLE e2e_users(id INT PRIMARY KEY, name VARCHAR(50) NOT NULL) */;
INSERT INTO e2e_users VALUES(1, /*!40101 'inline; comment' */);`,
				"inline; comment",
			},
			{
				"hash_comments",
				`# a comment; with unmatched quotes ' " and /*
CREATE TABLE e2e_users(id INT PRIMARY KEY, name VARCHAR(50) NOT NULL);
INSERT INTO e2e_users VALUES(1, '#; literal') # inline; comment
;
# trailing comment;`,
				"#; literal",
			},
			{
				"hash_delimiter",
				`DELIMITER #
CREATE TABLE e2e_users(id INT PRIMARY KEY, name VARCHAR(50) NOT NULL)#
INSERT INTO e2e_users VALUES(1, 'hash delimiter')#
DELIMITER ;`,
				"hash delimiter",
			},
			{
				"executable_comments_custom_delimiter",
				`DELIMITER //
/*!40101 CREATE TABLE e2e_users(id INT PRIMARY KEY, name VARCHAR(50) NOT NULL) *///
/*!40101 INSERT INTO e2e_users VALUES(1, 'custom;//delimiter') *///
DELIMITER ;`,
				"custom;//delimiter",
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				histories := make([][]HistoryRow, 2)
				for index, runner := range []string{"flyway", "gofly"} {
					w := NewWorkspace(t, target)
					w.Write("V1__comments.sql", c.sql)
					if runner == "flyway" {
						w.MustRunFlyway("migrate")
						histories[index] = w.ReadHistory("", lib.FlywayTable)
					} else {
						g := w.Gofly(nil)
						if _, err := g.Migrate(); err != nil {
							t.Fatal(err)
						}
						histories[index] = w.ReadHistory("", lib.DefaultGoflyTable)
					}
					connection, err := lib.Connect(w.url, target.User, target.Password, 0)
					if err != nil {
						t.Fatal(err)
					}
					var got string
					err = connection.DB().QueryRow("SELECT name FROM e2e_users WHERE id = 1").Scan(&got)
					connection.Close()
					if err != nil {
						t.Fatalf("%s result: %v", runner, err)
					}
					if got != c.want {
						t.Fatalf("%s stored %q, want %q", runner, got, c.want)
					}
				}
				AssertSameHistory(t, c.name, histories[0], histories[1])
			})
		}
	})
}
