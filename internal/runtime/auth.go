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

	"belm/internal/model"
)

const internalAuthUsersTable = "belm_auth_users"

func (r *Runtime) authConfig() model.AuthConfig {
	if r.App.Auth != nil {
		return *r.App.Auth
	}
	return model.AuthConfig{
		EmailField:      "email",
		RoleField:       "role",
		CodeTTLMinutes:  10,
		SessionTTLHours: 24,
		EmailTransport:  "console",
		EmailFrom:       "no-reply@belm.local",
		EmailSubject:    "Your Belm login code",
		SendmailPath:    "/usr/sbin/sendmail",
		DevExposeCode:   true,
	}
}

func (r *Runtime) usesAppAuthEntity() bool {
	return r.appAuthEnabled() && r.authUser != nil
}

// resolveAuth resolves a bearer token into an active session and hydrated auth user.
func (r *Runtime) resolveAuth(req *http.Request) (authSession, error) {
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
	user := userRow
	cfg := r.authConfig()
	role := any(nil)
	if r.usesAppAuthEntity() {
		user = decodeEntityRow(r.authUser, userRow)
		if cfg.RoleField != "" {
			role = user[cfg.RoleField]
		}
	} else {
		role = userRow["role"]
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

// parseBearerToken extracts the token from an Authorization header.
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
	cfg := r.authConfig()
	tableName := internalAuthUsersTable
	emailFieldName := cfg.EmailField
	if r.usesAppAuthEntity() {
		tableName = r.authUser.Table
	}
	table, _ := quoteIdentifier(tableName)
	emailField, _ := quoteIdentifier(emailFieldName)
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? COLLATE NOCASE LIMIT 1", table, emailField)
	return queryRow(r.DB, query, email)
}

func (r *Runtime) loadAuthUserByID(id any) (map[string]any, bool, error) {
	tableName := internalAuthUsersTable
	primaryKey := "id"
	if r.usesAppAuthEntity() {
		tableName = r.authUser.Table
		primaryKey = r.authUser.PrimaryKey
	}
	table, _ := quoteIdentifier(tableName)
	pk, _ := quoteIdentifier(primaryKey)
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", table, pk)
	return queryRow(r.DB, query, id)
}

func (r *Runtime) countAuthUsers() (int64, error) {
	tableName := internalAuthUsersTable
	if r.usesAppAuthEntity() {
		tableName = r.authUser.Table
	}
	table, _ := quoteIdentifier(tableName)
	row, ok, err := queryRow(r.DB, fmt.Sprintf("SELECT COUNT(*) AS total FROM %s", table))
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	total, _ := toInt64(row["total"])
	return total, nil
}

func (r *Runtime) countAuthUsersByRole(role string) (int64, error) {
	cfg := r.authConfig()
	if strings.TrimSpace(cfg.RoleField) == "" {
		return r.countAuthUsers()
	}
	tableName := internalAuthUsersTable
	if r.usesAppAuthEntity() {
		tableName = r.authUser.Table
	}
	table, _ := quoteIdentifier(tableName)
	roleField, _ := quoteIdentifier(cfg.RoleField)
	row, ok, err := queryRow(r.DB, fmt.Sprintf("SELECT COUNT(*) AS total FROM %s WHERE lower(%s) = lower(?)", table, roleField), role)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	total, _ := toInt64(row["total"])
	return total, nil
}

func parseAuthEmail(payload map[string]any) (string, error) {
	emailRaw, ok := payload["email"].(string)
	if !ok {
		return "", &apiError{Status: http.StatusBadRequest, Message: "email is required"}
	}
	email, err := normalizeAndValidateEmail(emailRaw)
	if err != nil {
		return "", &apiError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	return email, nil
}

// handleAuthRequestCode creates and delivers a one-time login code for an auth user email.
// If the user does not exist yet, Belm may auto-create it when the auth entity allows it.
func (r *Runtime) handleAuthRequestCode(w http.ResponseWriter, payload map[string]any) error {
	email, err := parseAuthEmail(payload)
	if err != nil {
		return err
	}

	user, found, err := r.loadOrCreateAuthUserForRequestCode(email)
	if err != nil {
		return err
	}
	if !found {
		r.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "If this email exists, a code was sent."})
		return nil
	}
	userID := user["id"]
	if r.usesAppAuthEntity() {
		userID = user[r.authUser.PrimaryKey]
	}
	return r.issueAuthCode(w, email, userID, "If this email exists, a code was sent.", "")
}

// handleBootstrapAdmin creates the first auth user and sends a verification code.
// The user is promoted to admin only after a successful login with that code.
func (r *Runtime) handleBootstrapAdmin(w http.ResponseWriter, payload map[string]any) error {
	cfg := r.authConfig()
	if strings.TrimSpace(cfg.RoleField) == "" {
		return &apiError{Status: http.StatusBadRequest, Message: "auth.role_field is required for admin bootstrap"}
	}

	totalUsers, err := r.countAuthUsers()
	if err != nil {
		return err
	}
	if totalUsers > 0 {
		return &apiError{Status: http.StatusConflict, Message: "Bootstrap is only allowed when there are no users"}
	}

	email, err := parseAuthEmail(payload)
	if err != nil {
		return err
	}

	user, found, err := r.tryAutoCreateAuthUser(email)
	if err != nil {
		return err
	}
	if !found {
		return &apiError{Status: http.StatusUnprocessableEntity, Message: "Could not create first user. Add optional/default fields or create one manually."}
	}
	userID := user["id"]
	if r.usesAppAuthEntity() {
		userID = user[r.authUser.PrimaryKey]
	}
	return r.issueAuthCode(w, email, userID, "First admin verification code sent. Complete login with this code to finish setup.", "admin")
}

func (r *Runtime) issueAuthCode(w http.ResponseWriter, email string, userID any, message string, grantRole string) error {
	cfg := r.authConfig()
	code, err := randomCode6()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	expiresAt := now + int64(cfg.CodeTTLMinutes)*60_000
	grantRole = strings.TrimSpace(grantRole)
	_, err = r.DB.Exec(
		`INSERT INTO belm_auth_codes (email, user_id, code, grant_role, expires_at, used, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)`,
		email,
		userID,
		code,
		grantRole,
		expiresAt,
		now,
	)
	if err != nil {
		return err
	}
	if err := r.deliverEmailCode(email, code); err != nil {
		return err
	}

	resp := map[string]any{"ok": true, "message": message}
	if cfg.DevExposeCode {
		resp["devCode"] = code
	}
	r.writeJSON(w, http.StatusOK, resp)
	return nil
}

// loadOrCreateAuthUserForRequestCode loads an auth user by email or auto-creates it when possible.
func (r *Runtime) loadOrCreateAuthUserForRequestCode(email string) (map[string]any, bool, error) {
	user, found, err := r.loadAuthUserByEmail(email)
	if err != nil || found {
		return user, found, err
	}
	totalUsers, err := r.countAuthUsers()
	if err != nil {
		return nil, false, err
	}
	if totalUsers == 0 {
		return r.tryAutoCreateAuthUserWithRole(email, "admin")
	}
	return r.tryAutoCreateAuthUser(email)
}

// tryAutoCreateAuthUser creates a minimal auth user for passwordless first-login flows.
// It only succeeds when all required fields can be safely inferred from auth config.
func (r *Runtime) tryAutoCreateAuthUser(email string) (map[string]any, bool, error) {
	return r.tryAutoCreateAuthUserWithRole(email, "user")
}

func (r *Runtime) tryAutoCreateAuthUserWithRole(email, roleValue string) (map[string]any, bool, error) {
	cfg := r.authConfig()
	if !r.usesAppAuthEntity() {
		user, found, err := r.loadAuthUserByEmail(email)
		if err != nil {
			return nil, false, err
		}
		if found {
			if strings.EqualFold(strings.TrimSpace(roleValue), "admin") {
				promoted, promoteErr := r.promoteAuthUserToAdmin(user)
				if promoteErr != nil {
					return nil, false, promoteErr
				}
				return promoted, true, nil
			}
			return user, true, nil
		}

		now := time.Now().UnixMilli()
		table, err := quoteIdentifier(internalAuthUsersTable)
		if err != nil {
			return nil, false, err
		}
		if _, err := r.DB.Exec(
			fmt.Sprintf("INSERT INTO %s (email, role, created_at) VALUES (?, ?, ?)", table),
			email,
			roleValue,
			now,
		); err != nil {
			user, found, loadErr := r.loadAuthUserByEmail(email)
			if loadErr == nil && found {
				return user, true, nil
			}
			return nil, false, err
		}
		return r.loadAuthUserByEmail(email)
	}

	columns := make([]string, 0, len(r.authUser.Fields))
	placeholders := make([]string, 0, len(r.authUser.Fields))
	values := make([]any, 0, len(r.authUser.Fields))
	ctx := entityNullContext(r.authUser)
	hasEmailField := false

	for _, field := range r.authUser.Fields {
		if field.Primary && field.Auto {
			continue
		}

		quoted, err := quoteIdentifier(field.Name)
		if err != nil {
			return nil, false, err
		}

		switch {
		case field.Name == cfg.EmailField:
			columns = append(columns, quoted)
			placeholders = append(placeholders, "?")
			values = append(values, email)
			ctx[field.Name] = email
			hasEmailField = true
		case cfg.RoleField != "" && field.Name == cfg.RoleField:
			if field.Type != "String" {
				return nil, false, nil
			}
			columns = append(columns, quoted)
			placeholders = append(placeholders, "?")
			values = append(values, roleValue)
			ctx[field.Name] = roleValue
		case field.Optional:
			// Keep optional fields nil for auto-provisioned users.
		default:
			// Required field that cannot be inferred automatically.
			return nil, false, nil
		}
	}

	if !hasEmailField || len(columns) == 0 {
		return nil, false, nil
	}

	if err := r.validateEntityRules(r.authUser, ctx); err != nil {
		return nil, false, nil
	}

	table, err := quoteIdentifier(r.authUser.Table)
	if err != nil {
		return nil, false, err
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(columns, ", "), strings.Join(placeholders, ", "))
	if _, err := r.DB.Exec(insertSQL, values...); err != nil {
		// If a concurrent request created the same user, load and continue.
		user, found, loadErr := r.loadAuthUserByEmail(email)
		if loadErr == nil && found {
			return user, true, nil
		}
		return nil, false, err
	}

	user, found, err := r.loadAuthUserByEmail(email)
	if err != nil {
		return nil, false, err
	}
	return user, found, nil
}

// handleAuthLogin verifies an email+code pair and issues a session token.
func (r *Runtime) handleAuthLogin(w http.ResponseWriter, payload map[string]any) error {
	email, err := parseAuthEmail(payload)
	if err != nil {
		return err
	}
	codeRaw, ok := payload["code"].(string)
	if !ok {
		return &apiError{Status: http.StatusBadRequest, Message: "code is required"}
	}
	code := strings.TrimSpace(codeRaw)
	if code == "" {
		return &apiError{Status: http.StatusBadRequest, Message: "code is required"}
	}

	row, ok, err := queryRow(r.DB, `SELECT id, user_id, code, grant_role, expires_at, used FROM belm_auth_codes WHERE email = ? ORDER BY id DESC LIMIT 1`, email)
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
	grantRole, _ := row["grant_role"].(string)

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

	if strings.EqualFold(strings.TrimSpace(grantRole), "admin") {
		promotedUser, promoteErr := r.promoteAuthUserToAdmin(userRow)
		if promoteErr != nil {
			return promoteErr
		}
		userRow = promotedUser
	}

	decodedUser := userRow
	if r.usesAppAuthEntity() {
		decodedUser = decodeEntityRow(r.authUser, userRow)
	}

	token, err := randomToken(32)
	if err != nil {
		return err
	}
	cfg := r.authConfig()
	sessionExpiresAt := now + int64(cfg.SessionTTLHours)*60*60*1000
	if _, err := r.DB.Exec(`INSERT INTO belm_sessions (token, user_id, email, expires_at, revoked, created_at) VALUES (?, ?, ?, ?, 0, ?)`, token, userID, email, sessionExpiresAt, now); err != nil {
		return err
	}

	loginRole := any(nil)
	if cfg.RoleField != "" {
		loginRole = decodedUser[cfg.RoleField]
	}
	r.writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"token":     token,
		"expiresAt": sessionExpiresAt,
		"email":     email,
		"role":      loginRole,
		"user":      decodedUser,
	})
	return nil
}

func (r *Runtime) promoteAuthUserToAdmin(user map[string]any) (map[string]any, error) {
	cfg := r.authConfig()
	if strings.TrimSpace(cfg.RoleField) == "" {
		return user, nil
	}

	tableName := internalAuthUsersTable
	primaryKey := "id"
	if r.usesAppAuthEntity() {
		tableName = r.authUser.Table
		primaryKey = r.authUser.PrimaryKey
	}
	table, err := quoteIdentifier(tableName)
	if err != nil {
		return nil, err
	}
	pk, err := quoteIdentifier(primaryKey)
	if err != nil {
		return nil, err
	}
	roleField, err := quoteIdentifier(cfg.RoleField)
	if err != nil {
		return nil, err
	}

	userID := user["id"]
	if r.usesAppAuthEntity() {
		userID = user[r.authUser.PrimaryKey]
	}
	if userID == nil {
		return user, nil
	}

	if _, err := r.DB.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", table, roleField, pk), "admin", userID); err != nil {
		return nil, err
	}
	updated, found, err := r.loadAuthUserByID(userID)
	if err != nil {
		return nil, err
	}
	if !found {
		return user, nil
	}
	return updated, nil
}

// handleAuthLogout revokes the caller session token.
func (r *Runtime) handleAuthLogout(w http.ResponseWriter, auth authSession) error {
	if !auth.Authenticated || auth.Token == "" {
		return &apiError{Status: http.StatusUnauthorized, Message: "Authentication required"}
	}
	if _, err := r.DB.Exec(`UPDATE belm_sessions SET revoked = 1 WHERE token = ?`, auth.Token); err != nil {
		return err
	}
	r.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// deliverEmailCode dispatches login codes through the configured transport.
func (r *Runtime) deliverEmailCode(toEmail, code string) error {
	cfg := r.authConfig()
	if cfg.DevExposeCode {
		r.printAuthLogHeader()
		r.printAuthLogSection("Code generated")
		r.printAuthLogFieldCommand("Dev code", code)
		r.printAuthLogField("Email", toEmail)
	}

	switch cfg.EmailTransport {
	case "console":
		r.printAuthLogHeader()
		r.printAuthLogSection("Email delivery")
		r.printAuthLogFieldCommand("Status", "sent")
		r.printAuthLogField("Transport", "console")
		r.printAuthLogField("To", toEmail)
		return nil
	case "sendmail":
		if err := sendWithSendmail(cfg.SendmailPath, cfg.EmailFrom, cfg.EmailSubject, toEmail, code, cfg.CodeTTLMinutes); err != nil {
			return err
		}
		r.printAuthLogHeader()
		r.printAuthLogSection("Email delivery")
		r.printAuthLogFieldCommand("Status", "sent")
		r.printAuthLogField("Transport", "sendmail")
		r.printAuthLogField("To", toEmail)
		return nil
	default:
		return fmt.Errorf("unsupported email transport %q", cfg.EmailTransport)
	}
}

func (r *Runtime) printAuthLogHeader() {
	useColor := supportsANSI()
	r.authLogOnce.Do(func() {
		fmt.Println()
		fmt.Printf("%s\n", colorize(useColor, ansiSection, "Auth logs"))
	})
}

func (r *Runtime) printAuthLogSection(title string) {
	useColor := supportsANSI()
	fmt.Printf("  %s\n", colorize(useColor, ansiSection, title))
}

func (r *Runtime) printAuthLogField(label, value string) {
	r.printAuthLogFieldWithColor(label, value, "")
}

func (r *Runtime) printAuthLogFieldCommand(label, value string) {
	r.printAuthLogFieldWithColor(label, value, ansiCommand)
}

func (r *Runtime) printAuthLogFieldWithColor(label, value, valueColor string) {
	useColor := supportsANSI()
	key := label + ":"
	const keyWidth = 12
	if len(key) < keyWidth {
		key += strings.Repeat(" ", keyWidth-len(key))
	}
	displayValue := value
	if valueColor != "" {
		displayValue = colorize(useColor, valueColor, value)
	}
	fmt.Printf("    %s %s\n", colorize(useColor, ansiLabel, key), displayValue)
}

// sendWithSendmail sends plain-text email by invoking the local sendmail binary.
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

// randomCode6 returns a zero-padded 6-digit cryptographically random code.
func randomCode6() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// randomToken returns a hex-encoded cryptographically random token.
func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
