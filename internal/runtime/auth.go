package runtime

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"mar/internal/model"
)

const sessionCookieName = "mar_session"
const adminUISessionHeader = "X-Mar-Admin-UI"

func (r *Runtime) authConfig() model.AuthConfig {
	var cfg model.AuthConfig
	if r.App.Auth != nil {
		cfg = *r.App.Auth
	} else {
		cfg = model.AuthConfig{
			UserEntity:      "User",
			EmailField:      "email",
			RoleField:       "role",
			CodeTTLMinutes:  10,
			SessionTTLHours: 24,
			EmailTransport:  "console",
			EmailFrom:       "no-reply@mar.local",
			EmailSubject:    "Your Mar login code",
			SMTPPort:        587,
			SMTPStartTLS:    true,
		}
	}

	if isMarDevMode() && strings.EqualFold(strings.TrimSpace(cfg.EmailTransport), "smtp") {
		cfg.EmailTransport = "console"
	}

	return cfg
}

// resolveAuth resolves a bearer token into an active session and hydrated auth user.
func (r *Runtime) resolveAuth(req *http.Request, requestID string) (authSession, error) {
	token := parseBearerToken(req.Header.Get("Authorization"))
	if token == "" {
		token = readSessionCookie(req)
	}
	if token == "" {
		return authSession{}, nil
	}
	tokenHash := hashAuthSecret(token)

	row, ok, err := queryRowForRequest(
		r.DB,
		requestID,
		`SELECT user_id, email, expires_at, revoked FROM mar_sessions WHERE token = ? LIMIT 1`,
		tokenHash,
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
	userRow, ok, err := r.loadAuthUserByID(requestID, userID)
	if err != nil {
		return authSession{}, err
	}
	if !ok {
		return authSession{}, nil
	}
	user := decodeEntityRow(r.authUser, userRow)
	cfg := r.authConfig()
	role := user[cfg.RoleField]

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

func readSessionCookie(req *http.Request) string {
	if req == nil {
		return ""
	}
	cookie, err := req.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func sessionCookieSecure(req *http.Request) bool {
	if req != nil && req.TLS != nil {
		return true
	}
	if req == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")), "https")
}

func writeSessionCookie(w http.ResponseWriter, req *http.Request, token string, expiresAtMillis int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    strings.TrimSpace(token),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   sessionCookieSecure(req),
		Expires:  time.UnixMilli(expiresAtMillis).UTC(),
		MaxAge:   max(1, int(time.Until(time.UnixMilli(expiresAtMillis)).Seconds())),
	})
}

func clearSessionCookie(w http.ResponseWriter, req *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   sessionCookieSecure(req),
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
	})
}

func (r *Runtime) requestedAdminUISessionTTLHours(req *http.Request) int {
	cfg := r.authConfig()
	defaultHours := cfg.SessionTTLHours
	if req == nil || !strings.EqualFold(strings.TrimSpace(req.Header.Get(adminUISessionHeader)), "true") {
		return defaultHours
	}
	if r.App != nil && r.App.System != nil && r.App.System.AdminUISessionTTLHours != nil {
		return *r.App.System.AdminUISessionTTLHours
	}
	return defaultHours
}

func (r *Runtime) loadAuthUserByEmail(requestID, email string) (map[string]any, bool, error) {
	cfg := r.authConfig()
	table, _ := quoteIdentifier(r.authUser.Table)
	emailField, _ := quoteIdentifier(cfg.EmailField)
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? COLLATE NOCASE LIMIT 1", table, emailField)
	return queryRowForRequest(r.DB, requestID, query, email)
}

func (r *Runtime) loadAuthUserByID(requestID string, id any) (map[string]any, bool, error) {
	table, _ := quoteIdentifier(r.authUser.Table)
	pk, _ := quoteIdentifier(r.authUser.PrimaryKey)
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", table, pk)
	return queryRowForRequest(r.DB, requestID, query, id)
}

func (r *Runtime) countAuthUsers(requestID string) (int64, error) {
	table, _ := quoteIdentifier(r.authUser.Table)
	row, ok, err := queryRowForRequest(r.DB, requestID, fmt.Sprintf("SELECT COUNT(*) AS total FROM %s", table))
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
		return "", newAPIError(http.StatusBadRequest, "email_required", "Email is required.")
	}
	email, err := normalizeAndValidateEmail(emailRaw)
	if err != nil {
		return "", newAPIError(http.StatusBadRequest, "invalid_email", err.Error())
	}
	return email, nil
}

// handleAuthRequestCode creates and delivers a one-time login code for an auth user email.
// If the user does not exist yet, Mar may auto-create it when the auth entity allows it.
func (r *Runtime) handleAuthRequestCode(w http.ResponseWriter, requestID string, payload map[string]any) error {
	email, err := parseAuthEmail(payload)
	if err != nil {
		return err
	}

	user, found, err := r.loadOrCreateAuthUserForRequestCode(requestID, email)
	if err != nil {
		return err
	}
	if !found {
		r.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "If this email exists, a code was sent."})
		return nil
	}
	userID := user[r.authUser.PrimaryKey]
	return r.issueAuthCode(w, requestID, email, userID, "If this email exists, a code was sent.", "")
}

// handleBootstrapAdmin creates the first auth user and sends a verification code.
// The user is promoted to admin only after a successful login with that code.
func (r *Runtime) handleBootstrapAdmin(w http.ResponseWriter, requestID string, payload map[string]any) error {
	totalUsers, err := r.countAuthUsers(requestID)
	if err != nil {
		return err
	}
	if totalUsers > 0 {
		return newAPIError(http.StatusConflict, "bootstrap_not_allowed", "Bootstrap is only allowed when there are no users")
	}

	email, err := parseAuthEmail(payload)
	if err != nil {
		return err
	}

	user, found, err := r.tryAutoCreateAuthUser(requestID, email)
	if err != nil {
		return err
	}
	if !found {
		return newAPIError(http.StatusUnprocessableEntity, "bootstrap_user_creation_failed", "Could not create first user. Add optional/default fields or create one manually.")
	}
	userID := user["id"]
	return r.issueAuthCode(w, requestID, email, userID, "First admin verification code sent. Complete login with this code to finish setup.", "admin")
}

func (r *Runtime) issueAuthCode(w http.ResponseWriter, requestID, email string, userID any, message string, grantRole string) error {
	cfg := r.authConfig()
	code, err := randomCode6()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	expiresAt := now + int64(cfg.CodeTTLMinutes)*60_000
	grantRole = strings.TrimSpace(grantRole)
	_, err = r.DB.ExecTagged(
		requestID,
		`INSERT INTO mar_auth_codes (email, user_id, code, grant_role, expires_at, used, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)`,
		email,
		userID,
		hashAuthSecret(code),
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

	responseMessage := message
	if isMarDevMode() && strings.EqualFold(strings.TrimSpace(cfg.EmailTransport), "console") {
		responseMessage = "Login code generated. You are running in dev mode with email transport set to console, so check there."
	}

	r.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": responseMessage})
	return nil
}

func isMarDevMode() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("MAR_DEV_MODE")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

// loadOrCreateAuthUserForRequestCode loads an auth user by email or auto-creates it when possible.
func (r *Runtime) loadOrCreateAuthUserForRequestCode(requestID, email string) (map[string]any, bool, error) {
	user, found, err := r.loadAuthUserByEmail(requestID, email)
	if err != nil || found {
		return user, found, err
	}
	totalUsers, err := r.countAuthUsers(requestID)
	if err != nil {
		return nil, false, err
	}
	if totalUsers == 0 {
		return r.tryAutoCreateAuthUserWithRole(requestID, email, "admin")
	}
	return r.tryAutoCreateAuthUser(requestID, email)
}

// tryAutoCreateAuthUser creates a minimal auth user for passwordless first-login flows.
// It only succeeds when all required fields can be safely inferred from auth config.
func (r *Runtime) tryAutoCreateAuthUser(requestID, email string) (map[string]any, bool, error) {
	return r.tryAutoCreateAuthUserWithRole(requestID, email, "user")
}

func (r *Runtime) tryAutoCreateAuthUserWithRole(requestID, email, roleValue string) (map[string]any, bool, error) {
	cfg := r.authConfig()
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
		case field.Default != nil:
			dbValue, apiValue, normalizeErr := normalizeInputValue(&field, field.Default)
			if normalizeErr != nil {
				return nil, false, normalizeErr
			}
			columns = append(columns, quoted)
			placeholders = append(placeholders, "?")
			values = append(values, dbValue)
			ctx[field.Name] = apiValue
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
	if _, err := r.DB.ExecTagged(requestID, insertSQL, values...); err != nil {
		// If a concurrent request created the same user, load and continue.
		user, found, loadErr := r.loadAuthUserByEmail(requestID, email)
		if loadErr == nil && found {
			return user, true, nil
		}
		return nil, false, err
	}

	user, found, err := r.loadAuthUserByEmail(requestID, email)
	if err != nil {
		return nil, false, err
	}
	return user, found, nil
}

// handleAuthLogin verifies an email+code pair and issues a session token.
func (r *Runtime) handleAuthLogin(w http.ResponseWriter, req *http.Request, requestID string, payload map[string]any) error {
	email, err := parseAuthEmail(payload)
	if err != nil {
		return err
	}
	codeRaw, ok := payload["code"].(string)
	if !ok {
		return newAPIError(http.StatusBadRequest, "code_required", "Code is required.")
	}
	code := strings.TrimSpace(codeRaw)
	if code == "" {
		return newAPIError(http.StatusBadRequest, "code_required", "Code is required.")
	}

	row, ok, err := queryRowForRequest(r.DB, requestID, `SELECT id, user_id, code, grant_role, expires_at, used FROM mar_auth_codes WHERE email = ? ORDER BY id DESC LIMIT 1`, email)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if !ok {
		return newAPIError(http.StatusUnauthorized, "invalid_or_expired_code", "That code is invalid or expired. Request a new one and try again.")
	}
	used, _ := toInt64(row["used"])
	expiresAt, _ := toInt64(row["expires_at"])
	storedCode, _ := row["code"].(string)
	if used == 1 || expiresAt < now || !storedSecretMatches(storedCode, code) {
		return newAPIError(http.StatusUnauthorized, "invalid_or_expired_code", "That code is invalid or expired. Request a new one and try again.")
	}
	codeID, _ := toInt64(row["id"])
	userID := row["user_id"]
	grantRole, _ := row["grant_role"].(string)

	if _, err := r.DB.ExecTagged(requestID, `UPDATE mar_auth_codes SET used = 1 WHERE id = ?`, codeID); err != nil {
		return err
	}
	userRow, found, err := r.loadAuthUserByID(requestID, userID)
	if err != nil {
		return err
	}
	if !found {
		return newAPIError(http.StatusUnauthorized, "invalid_or_expired_code", "That code is invalid or expired. Request a new one and try again.")
	}

	if strings.EqualFold(strings.TrimSpace(grantRole), "admin") {
		promotedUser, promoteErr := r.promoteAuthUserToAdmin(requestID, userRow)
		if promoteErr != nil {
			return promoteErr
		}
		userRow = promotedUser
	}

	decodedUser := decodeEntityRow(r.authUser, userRow)

	token, err := randomToken(32)
	if err != nil {
		return err
	}
	tokenHash := hashAuthSecret(token)
	cfg := r.authConfig()
	sessionTTLHours := r.requestedAdminUISessionTTLHours(req)
	sessionExpiresAt := now + int64(sessionTTLHours)*60*60*1000
	if _, err := r.DB.ExecTagged(requestID, `INSERT INTO mar_sessions (token, user_id, email, expires_at, revoked, created_at) VALUES (?, ?, ?, ?, 0, ?)`, tokenHash, userID, email, sessionExpiresAt, now); err != nil {
		return err
	}
	writeSessionCookie(w, req, token, sessionExpiresAt)

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

func (r *Runtime) promoteAuthUserToAdmin(requestID string, user map[string]any) (map[string]any, error) {
	cfg := r.authConfig()
	if strings.TrimSpace(cfg.RoleField) == "" {
		return user, nil
	}

	table, err := quoteIdentifier(r.authUser.Table)
	if err != nil {
		return nil, err
	}
	pk, err := quoteIdentifier(r.authUser.PrimaryKey)
	if err != nil {
		return nil, err
	}
	roleField, err := quoteIdentifier(cfg.RoleField)
	if err != nil {
		return nil, err
	}

	userID := user[r.authUser.PrimaryKey]
	if userID == nil {
		return user, nil
	}

	if _, err := r.DB.ExecTagged(requestID, fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", table, roleField, pk), "admin", userID); err != nil {
		return nil, err
	}
	updated, found, err := r.loadAuthUserByID(requestID, userID)
	if err != nil {
		return nil, err
	}
	if !found {
		return user, nil
	}
	return updated, nil
}

// handleAuthLogout revokes the caller session token.
func (r *Runtime) handleAuthLogout(w http.ResponseWriter, req *http.Request, requestID string, auth authSession) error {
	if !auth.Authenticated || auth.Token == "" {
		return newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
	}
	if _, err := r.DB.ExecTagged(requestID, `UPDATE mar_sessions SET revoked = 1 WHERE token = ?`, hashAuthSecret(auth.Token)); err != nil {
		return err
	}
	clearSessionCookie(w, req)
	r.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// deliverEmailCode dispatches login codes through the configured transport.
func (r *Runtime) deliverEmailCode(toEmail, code string) error {
	cfg := r.authConfig()

	switch cfg.EmailTransport {
	case "console":
		body := buildAuthEmailBody(code, cfg.CodeTTLMinutes)
		r.printAuthLogHeader()
		r.printAuthLogSection("Login code delivery")
		r.printAuthLogFieldCommand("Status", "sent")
		r.printAuthLogField("Transport", "console")
		r.printAuthLogField("To", toEmail)
		r.printAuthLogSection("Email body")
		r.printAuthLogMultiline(body)
		return nil
	case "smtp":
		if err := sendWithSMTP(cfg, toEmail, code); err != nil {
			return err
		}
		r.printAuthLogHeader()
		r.printAuthLogSection("Login code delivery")
		r.printAuthLogFieldCommand("Status", "sent")
		r.printAuthLogField("Transport", "smtp")
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

func (r *Runtime) printAuthLogMultiline(content string) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fmt.Printf("    %s\n", line)
	}
}

func buildAuthEmailMessage(from, subject, to, code string, ttlMinutes int) string {
	return strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"Content-Type: text/plain; charset=utf-8",
		"",
		buildAuthEmailBody(code, ttlMinutes),
	}, "\n")
}

func buildAuthEmailBody(code string, ttlMinutes int) string {
	return strings.Join([]string{
		"Your login code is:",
		code,
		"",
		fmt.Sprintf("This code expires in %d minute(s).", ttlMinutes),
		"",
		"If you did not request this code, ignore this email.",
	}, "\n")
}
