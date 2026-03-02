package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (r *Runtime) resolveAuth(req *http.Request) (authSession, error) {
	if !r.authEnabled() || r.authUser == nil {
		return authSession{}, nil
	}
	token := parseBearerToken(req.Header.Get("Authorization"))
	if token == "" {
		return authSession{}, nil
	}

	row, ok, err := queryRow(
		r.DB,
		`SELECT token, user_id, email, expires_at, revoked FROM belm_sessions WHERE token = ? LIMIT 1`,
		token,
	)
	if err != nil {
		return authSession{}, err
	}
	if !ok {
		return authSession{}, nil
	}
	if revoked, _ := toInt64(row["revoked"]); revoked == 1 {
		return authSession{}, nil
	}
	expiresAt, _ := toInt64(row["expires_at"])
	if expiresAt < time.Now().UnixMilli() {
		return authSession{}, nil
	}

	userID := row["user_id"]
	userRow, ok, err := r.loadAuthUserByID(userID)
	if err != nil {
		return authSession{}, err
	}
	if !ok {
		return authSession{}, nil
	}
	user := decodeEntityRow(r.authUser, userRow)
	role := any(nil)
	if r.App.Auth.RoleField != "" {
		role = user[r.App.Auth.RoleField]
	}

	email, _ := row["email"].(string)
	return authSession{
		Authenticated: true,
		Token:         token,
		Email:         email,
		UserID:        userID,
		Role:          role,
		User:          user,
	}, nil
}

func parseBearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func (r *Runtime) loadAuthUserByEmail(email string) (map[string]any, bool, error) {
	table, _ := quoteIdentifier(r.authUser.Table)
	emailField, _ := quoteIdentifier(r.App.Auth.EmailField)
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", table, emailField)
	return queryRow(r.DB, query, email)
}

func (r *Runtime) loadAuthUserByID(id any) (map[string]any, bool, error) {
	table, _ := quoteIdentifier(r.authUser.Table)
	pk, _ := quoteIdentifier(r.authUser.PrimaryKey)
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", table, pk)
	return queryRow(r.DB, query, id)
}

func (r *Runtime) handleAuthRequestCode(w http.ResponseWriter, payload map[string]any) error {
	if !r.authEnabled() {
		return &apiError{Status: http.StatusNotFound, Message: "Authentication is not enabled"}
	}
	emailRaw, ok := payload["email"].(string)
	if !ok {
		return &apiError{Status: http.StatusBadRequest, Message: "email is required"}
	}
	email := normalizeEmail(emailRaw)
	if email == "" {
		return &apiError{Status: http.StatusBadRequest, Message: "email is required"}
	}

	user, found, err := r.loadAuthUserByEmail(email)
	if err != nil {
		return err
	}
	if !found {
		r.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "If this email exists, a code was sent."})
		return nil
	}
	userID := user[r.authUser.PrimaryKey]
	code, err := randomCode6()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	expiresAt := now + int64(r.App.Auth.CodeTTLMinutes)*60_000
	_, err = r.DB.Exec(`INSERT INTO belm_auth_codes (email, user_id, code, expires_at, used, created_at) VALUES (?, ?, ?, ?, 0, ?)`, email, userID, code, expiresAt, now)
	if err != nil {
		return err
	}
	if err := r.deliverEmailCode(email, code); err != nil {
		return err
	}

	resp := map[string]any{"ok": true, "message": "If this email exists, a code was sent."}
	if r.App.Auth.DevExposeCode {
		resp["devCode"] = code
	}
	r.writeJSON(w, http.StatusOK, resp)
	return nil
}

func (r *Runtime) handleAuthLogin(w http.ResponseWriter, payload map[string]any) error {
	if !r.authEnabled() {
		return &apiError{Status: http.StatusNotFound, Message: "Authentication is not enabled"}
	}
	emailRaw, ok := payload["email"].(string)
	if !ok {
		return &apiError{Status: http.StatusBadRequest, Message: "email is required"}
	}
	codeRaw, ok := payload["code"].(string)
	if !ok {
		return &apiError{Status: http.StatusBadRequest, Message: "code is required"}
	}
	email := normalizeEmail(emailRaw)
	code := strings.TrimSpace(codeRaw)
	if email == "" {
		return &apiError{Status: http.StatusBadRequest, Message: "email is required"}
	}
	if code == "" {
		return &apiError{Status: http.StatusBadRequest, Message: "code is required"}
	}

	row, ok, err := queryRow(r.DB, `SELECT id, user_id, code, expires_at, used FROM belm_auth_codes WHERE email = ? ORDER BY id DESC LIMIT 1`, email)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if !ok {
		return &apiError{Status: http.StatusUnauthorized, Message: "Invalid or expired code"}
	}
	used, _ := toInt64(row["used"])
	expiresAt, _ := toInt64(row["expires_at"])
	storedCode, _ := row["code"].(string)
	if used == 1 || expiresAt < now || storedCode != code {
		return &apiError{Status: http.StatusUnauthorized, Message: "Invalid or expired code"}
	}
	codeID, _ := toInt64(row["id"])
	userID := row["user_id"]

	if _, err := r.DB.Exec(`UPDATE belm_auth_codes SET used = 1 WHERE id = ?`, codeID); err != nil {
		return err
	}
	userRow, found, err := r.loadAuthUserByID(userID)
	if err != nil {
		return err
	}
	if !found {
		return &apiError{Status: http.StatusUnauthorized, Message: "Invalid or expired code"}
	}

	token, err := randomToken(32)
	if err != nil {
		return err
	}
	sessionExpiresAt := now + int64(r.App.Auth.SessionTTLHours)*60*60*1000
	if _, err := r.DB.Exec(`INSERT INTO belm_sessions (token, user_id, email, expires_at, revoked, created_at) VALUES (?, ?, ?, ?, 0, ?)`, token, userID, email, sessionExpiresAt, now); err != nil {
		return err
	}

	r.writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"token":     token,
		"expiresAt": sessionExpiresAt,
		"user":      decodeEntityRow(r.authUser, userRow),
	})
	return nil
}

func (r *Runtime) handleAuthLogout(w http.ResponseWriter, auth authSession) error {
	if !r.authEnabled() {
		return &apiError{Status: http.StatusNotFound, Message: "Authentication is not enabled"}
	}
	if !auth.Authenticated || auth.Token == "" {
		return &apiError{Status: http.StatusUnauthorized, Message: "Authentication required"}
	}
	if _, err := r.DB.Exec(`UPDATE belm_sessions SET revoked = 1 WHERE token = ?`, auth.Token); err != nil {
		return err
	}
	r.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (r *Runtime) deliverEmailCode(toEmail, code string) error {
	switch r.App.Auth.EmailTransport {
	case "console":
		fmt.Printf("[BelmAuthEmail] to=%s code=%s\n", toEmail, code)
		return nil
	case "sendmail":
		return sendWithSendmail(r.App.Auth.SendmailPath, r.App.Auth.EmailFrom, r.App.Auth.EmailSubject, toEmail, code, r.App.Auth.CodeTTLMinutes)
	default:
		return fmt.Errorf("unsupported email transport %q", r.App.Auth.EmailTransport)
	}
}

func sendWithSendmail(sendmailPath, from, subject, to, code string, ttlMinutes int) error {
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Your login code is:",
		code,
		"",
		fmt.Sprintf("This code expires in %d minute(s).", ttlMinutes),
		"",
		"If you did not request this code, ignore this email.",
	}, "\n")

	cmd := exec.Command(sendmailPath, "-t", "-i")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := stdin.Write([]byte(msg)); err != nil {
		_ = stdin.Close()
		return err
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return err
	}
	return nil
}

func randomCode6() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
