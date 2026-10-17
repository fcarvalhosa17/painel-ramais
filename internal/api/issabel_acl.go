package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite" // driver "sqlite", o mesmo do store
)

// DefaultIssabelACLDB e o banco de usuarios e grupos do Issabel 5 (docs/fase2/leitura-vm.txt).
const DefaultIssabelACLDB = "/var/www/db/acl.db"

// issabelAdminGroup e o nome do grupo de administradores no acl.db (id 1 na VM).
const issabelAdminGroup = "administrator"

// IssabelACL devolve a conferencia usada antes de cada escrita de grupo (M1): o papel
// admin vem do token e fica na sessao, que nao expira enquanto a TV usa a tela; por isso o
// daemon rele o acl.db a cada escrita. Somente leitura (mode=ro nunca cria o arquivo).
func IssabelACL(path string) func(ctx context.Context, user string) (bool, error) {
	return func(ctx context.Context, user string) (bool, error) {
		db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(1000)")
		if err != nil {
			return false, fmt.Errorf("abrir %s: %w", path, err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		var n int
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM acl_user u
			JOIN acl_membership m ON m.id_user = u.id
			JOIN acl_group g ON g.id = m.id_group
			WHERE u.name = ? AND g.name = ?`, user, issabelAdminGroup).Scan(&n)
		if err != nil {
			return false, fmt.Errorf("ler %s: %w", path, err)
		}
		return n > 0, nil
	}
}
