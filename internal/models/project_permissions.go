package models

import "database/sql"

type ProjectPermission struct {
	UserID                          int64
	Username, FullName, AccessLevel string
}

// CanAccessProject reports the effective project access for a non-owner user.
// A write grant also includes read access.
func CanAccessProject(d *sql.DB, projectID, userID int64, write bool) (bool, error) {
	var ok bool
	q := `SELECT EXISTS(SELECT 1 FROM project_permissions WHERE project_id=$1 AND user_id=$2`
	if write {
		q += ` AND access_level='write'`
	}
	q += `)`
	err := d.QueryRow(q, projectID, userID).Scan(&ok)
	return ok, err
}

func GrantProjectAccess(d *sql.DB, projectID, userID int64, level string) error {
	_, err := d.Exec(`INSERT INTO project_permissions(project_id,user_id,access_level) VALUES($1,$2,$3)
		ON CONFLICT(project_id,user_id) DO UPDATE SET access_level=excluded.access_level`, projectID, userID, level)
	return err
}

func RevokeProjectAccess(d *sql.DB, projectID, userID int64) error {
	_, err := d.Exec(`DELETE FROM project_permissions WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	return err
}

func ListProjectPermissions(d *sql.DB, projectID int64) ([]ProjectPermission, error) {
	rows, err := d.Query(`SELECT u.id,u.username,u.full_name,p.access_level FROM project_permissions p JOIN users u ON u.id=p.user_id WHERE p.project_id=$1 ORDER BY u.username`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectPermission
	for rows.Next() {
		var p ProjectPermission
		if err := rows.Scan(&p.UserID, &p.Username, &p.FullName, &p.AccessLevel); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
