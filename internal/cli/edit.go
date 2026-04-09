//go:build !windows

package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"mar/internal/expr"
	"mar/internal/formatter"

	"golang.org/x/sys/unix"
)

const (
	editorKeyArrowLeft = 1000 + iota
	editorKeyArrowRight
	editorKeyArrowUp
	editorKeyArrowDown
	editorKeyDelete
	editorKeyHome
	editorKeyEnd
	editorKeyPageUp
	editorKeyPageDown
	editorKeyMouseWheelUp
	editorKeyMouseWheelDown
	editorKeyRedo
)

type marEditor struct {
	filePath   string
	lines      []string
	savedLines []string
	gitBase    []string
	gitSigns   map[int]rune
	cx         int
	cy         int
	rowOffset  int
	colOffset  int
	screenRows int
	screenCols int
	dirty      bool
	status     string
	statusTime time.Time
	quitArmed  bool
	useColor   bool
	clipboard  string
	selecting  bool
	selectX    int
	selectY    int
	undoStack  []editorSnapshot
	redoStack  []editorSnapshot
}

type editorPos struct {
	x int
	y int
}

type editorSnapshot struct {
	lines []string
	cx    int
	cy    int
}

var (
	marEditorKeywords = map[string]struct{}{
		"app": {}, "port": {}, "database": {}, "ios": {}, "public": {}, "system": {}, "auth": {}, "frontend": {}, "screen": {}, "section": {}, "link": {}, "to": {},
		"list": {}, "children": {}, "by": {}, "destination": {}, "field": {}, "title": {}, "subtitle": {}, "entity": {}, "action": {}, "type": {}, "alias": {},
		"rule": {}, "expect": {}, "authorize": {}, "when": {}, "read": {}, "load": {}, "create": {}, "update": {}, "delete": {}, "belongs_to": {}, "current_user": {}, "ref": {},
		"bundle_identifier": {}, "display_name": {}, "server_url": {},
		"code_ttl_minutes": {}, "session_ttl_hours": {}, "email_from": {}, "email_subject": {}, "smtp_host": {}, "smtp_port": {}, "smtp_username": {}, "smtp_password_env": {}, "smtp_starttls": {},
		"dir": {}, "mount": {}, "spa_fallback": {},
		"request_logs_buffer": {}, "http_max_request_body_mb": {}, "auth_request_code_rate_limit_per_minute": {}, "auth_login_rate_limit_per_minute": {}, "admin_ui_session_ttl_hours": {},
		"security_frame_policy": {}, "security_referrer_policy": {}, "security_content_type_nosniff": {}, "sqlite_journal_mode": {}, "sqlite_synchronous": {}, "sqlite_foreign_keys": {},
		"sqlite_busy_timeout_ms": {}, "sqlite_wal_autocheckpoint": {}, "sqlite_journal_size_limit_mb": {}, "sqlite_mmap_size_mb": {}, "sqlite_cache_size_kb": {},
	}
	marEditorFunctions = map[string]struct{}{
		"length": {}, "contains": {}, "starts_with": {}, "ends_with": {}, "matches": {},
	}
	marEditorLogicalOperators = map[string]struct{}{
		"and": {}, "or": {}, "not": {},
	}
	marEditorTypes = map[string]struct{}{
		"String": {}, "Int": {}, "Bool": {}, "Float": {}, "Date": {}, "DateTime": {},
	}
	marEditorFieldModifiers = map[string]struct{}{
		"primary": {}, "auto": {}, "optional": {}, "default": {},
	}
	marEditorLiterals = map[string]struct{}{
		"true": {}, "false": {}, "null": {},
	}
)

func runEdit(binaryName string, args []string) error {
	if len(args) != 1 {
		return editUsageError(binaryName)
	}

	if !stdinIsTerminal() || !stdoutIsTerminal() {
		return initCLIError(
			"Interactive terminal required",
			"The editor can only run inside an interactive terminal.",
			fmt.Sprintf("Run %s edit <app.mar> in a normal terminal window.", binaryName),
		)
	}

	editor, err := openMarEditor(args[0])
	if err != nil {
		return err
	}
	return editor.run()
}

func editUsageError(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Edit usage"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s edit <app.mar>", binaryName))
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Open a Mar file in the terminal with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s edit app.mar", binaryName)))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func openMarEditor(path string) (*marEditor, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, editUsageError("mar")
	}
	if filepath.Ext(trimmed) != ".mar" {
		return nil, initCLIError(
			"Invalid Mar file",
			fmt.Sprintf("%s is not a .mar file.", trimmed),
			"Use a file name like app.mar.",
		)
	}

	lines := []string{""}
	if content, err := os.ReadFile(trimmed); err == nil {
		lines = splitEditorLines(string(content))
	} else if !os.IsNotExist(err) {
		return nil, initCLIError(
			"Cannot open file",
			err.Error(),
			"Check that the file exists and that you have permission to read it.",
		)
	}

	rows, cols := editorTerminalSize()
	editor := &marEditor{
		filePath:   trimmed,
		lines:      lines,
		savedLines: cloneEditorLines(lines),
		gitBase:    gitBaseLines(trimmed),
		gitSigns:   nil,
		screenRows: rows,
		screenCols: cols,
		useColor:   cliSupportsANSIStream(os.Stdout),
		status:     "",
		statusTime: time.Now(),
	}
	editor.updateGitSigns()
	return editor, nil
}

func splitEditorLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func editorTerminalSize() (int, int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Row == 0 || ws.Col == 0 {
		return 22, 80
	}
	rows := int(ws.Row) - 2
	if rows < 5 {
		rows = 5
	}
	return rows, int(ws.Col)
}

func (e *marEditor) run() error {
	oldState, err := editorEnableRawMode()
	if err != nil {
		return initCLIError(
			"Cannot start editor",
			err.Error(),
			"Try running this command in a normal terminal window.",
		)
	}
	defer editorDisableRawMode(oldState)

	fmt.Print("\x1b[?1049h\x1b[?1000h\x1b[?1002h\x1b[?1006h\x1b[?25l")
	defer fmt.Print("\x1b[?25h\x1b[?1006l\x1b[?1002l\x1b[?1000l\x1b[?1049l")

	for {
		e.refreshScreen()
		key, err := editorReadKey()
		if err != nil {
			return err
		}
		if shouldQuit, err := e.processKey(key); err != nil {
			return err
		} else if shouldQuit {
			return nil
		}
	}
}

func editorEnableRawMode() (*unix.Termios, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := unix.IoctlGetTermios(fd, editorTermiosGetReq)
	if err != nil {
		return nil, err
	}
	newState := *oldState
	newState.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	newState.Oflag &^= unix.OPOST
	newState.Cflag |= unix.CS8
	newState.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	newState.Cc[unix.VMIN] = 1
	newState.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, editorTermiosSetReq, &newState); err != nil {
		return nil, err
	}
	return oldState, nil
}

func editorDisableRawMode(oldState *unix.Termios) {
	if oldState == nil {
		return
	}
	_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), editorTermiosSetReq, oldState)
}

func editorReadKey() (int, error) {
	b, err := editorReadByte()
	if err != nil {
		return 0, err
	}
	if b != '\x1b' {
		return int(b), nil
	}

	b1, ok, err := editorReadByteWithTimeout(25 * time.Millisecond)
	if err != nil {
		return 0, err
	}
	if !ok {
		return int('\x1b'), nil
	}
	if b1 == '[' {
		b2, ok, err := editorReadByteWithTimeout(25 * time.Millisecond)
		if err != nil {
			return 0, err
		}
		if !ok {
			return int('\x1b'), nil
		}
		if b2 == '<' {
			return editorReadMouseEvent()
		}
		if b2 >= '0' && b2 <= '9' {
			var seq strings.Builder
			seq.WriteByte(b2)
			for {
				b, err := editorReadByte()
				if err != nil {
					return int('\x1b'), nil
				}
				seq.WriteByte(b)
				if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
					break
				}
			}
			return editorParseCSISequence(seq.String()), nil
		}
		switch b2 {
		case 'A':
			return editorKeyArrowUp, nil
		case 'B':
			return editorKeyArrowDown, nil
		case 'C':
			return editorKeyArrowRight, nil
		case 'D':
			return editorKeyArrowLeft, nil
		case 'H':
			return editorKeyHome, nil
		case 'F':
			return editorKeyEnd, nil
		}
	}
	if b1 == 'O' {
		b2, ok, err := editorReadByteWithTimeout(25 * time.Millisecond)
		if err != nil {
			return 0, err
		}
		if !ok {
			return int('\x1b'), nil
		}
		switch b2 {
		case 'H':
			return editorKeyHome, nil
		case 'F':
			return editorKeyEnd, nil
		}
	}
	return int('\x1b'), nil
}

func editorReadByteWithTimeout(timeout time.Duration) (byte, bool, error) {
	fd := int(os.Stdin.Fd())
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	ms := int(timeout / time.Millisecond)
	for {
		n, err := unix.Poll(poll, ms)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, false, err
		}
		if n == 0 || poll[0].Revents&unix.POLLIN == 0 {
			return 0, false, nil
		}
		b, err := editorReadByte()
		if err != nil {
			return 0, false, err
		}
		return b, true, nil
	}
}

func editorParseCSISequence(seq string) int {
	if strings.HasSuffix(seq, "~") {
		switch strings.TrimSuffix(seq, "~") {
		case "1", "7":
			return editorKeyHome
		case "3":
			return editorKeyDelete
		case "4", "8":
			return editorKeyEnd
		case "5":
			return editorKeyPageUp
		case "6":
			return editorKeyPageDown
		}
		return int('\x1b')
	}

	if len(seq) == 0 {
		return int('\x1b')
	}

	final := seq[len(seq)-1]
	body := seq[:len(seq)-1]
	parts := strings.Split(body, ";")
	switch final {
	case 'u':
		if len(parts) == 2 {
			code, _ := strconv.Atoi(parts[0])
			mod, _ := strconv.Atoi(parts[1])
			if mod == 6 && (code == 90 || code == 122) {
				return editorKeyRedo
			}
		}
	}
	return int('\x1b')
}

func editorReadMouseEvent() (int, error) {
	var seq strings.Builder
	for {
		b, err := editorReadByte()
		if err != nil {
			return int('\x1b'), nil
		}
		seq.WriteByte(b)
		if b == 'M' || b == 'm' {
			break
		}
	}

	body := strings.TrimSuffix(strings.TrimSuffix(seq.String(), "M"), "m")
	parts := strings.Split(body, ";")
	if len(parts) < 3 {
		return int('\x1b'), nil
	}
	button, err := strconv.Atoi(parts[0])
	if err != nil {
		return int('\x1b'), nil
	}

	switch button {
	case 64:
		return editorKeyMouseWheelUp, nil
	case 65:
		return editorKeyMouseWheelDown, nil
	default:
		return int('\x1b'), nil
	}
}

func editorReadByte() (byte, error) {
	var buf [1]byte
	_, err := os.Stdin.Read(buf[:])
	return buf[0], err
}

func editorCtrlKey(r byte) int {
	return int(r & 0x1f)
}

func (e *marEditor) processKey(key int) (bool, error) {
	switch key {
	case 0:
		e.quitArmed = false
		if e.selecting {
			e.clearSelection()
			e.setStatusMessage("Selection cleared")
		} else {
			e.beginSelection()
			e.setStatusMessage("Selection started")
		}
		return false, nil
	case editorCtrlKey('q'):
		if e.dirty && !e.quitArmed {
			e.quitArmed = true
			e.setStatusMessage("Unsaved changes. Press Ctrl-q again to quit.")
			return false, nil
		}
		return true, nil
	case editorCtrlKey('s'):
		if err := e.save(); err != nil {
			e.setStatusMessage("Save failed: " + err.Error())
			return false, nil
		}
		return false, nil
	case editorCtrlKey('c'):
		e.quitArmed = false
		if !e.hasSelection() {
			e.setStatusMessage("No selection to copy")
			return false, nil
		}
		e.clipboard = e.selectedText()
		e.clearSelection()
		if err := writeSystemClipboard(e.clipboard); err != nil {
			e.setStatusMessage("Selection copied (editor clipboard only)")
		} else {
			e.setStatusMessage("Selection copied")
		}
		return false, nil
	case editorCtrlKey('x'):
		e.quitArmed = false
		if !e.hasSelection() {
			e.setStatusMessage("No selection to cut")
			return false, nil
		}
		e.clipboard = e.selectedText()
		systemClipboardErr := writeSystemClipboard(e.clipboard)
		e.beginUndoGroup()
		e.deleteSelection()
		if systemClipboardErr != nil {
			e.setStatusMessage("Selection cut (editor clipboard only)")
		} else {
			e.setStatusMessage("Selection cut")
		}
		return false, nil
	case editorCtrlKey('v'):
		e.quitArmed = false
		if systemClipboard, err := readSystemClipboard(); err == nil {
			e.clipboard = systemClipboard
		}
		if e.clipboard == "" {
			e.setStatusMessage("Clipboard is empty")
			return false, nil
		}
		e.beginUndoGroup()
		if e.hasSelection() {
			e.deleteSelection()
		}
		e.insertText(e.clipboard)
		e.setStatusMessage("Pasted")
		return false, nil
	case editorCtrlKey('z'):
		e.quitArmed = false
		e.undo()
		return false, nil
	case editorCtrlKey('y'):
		e.quitArmed = false
		e.redo()
		return false, nil
	case editorKeyRedo:
		e.quitArmed = false
		e.redo()
		return false, nil
	case editorKeyArrowUp, editorKeyArrowDown, editorKeyArrowLeft, editorKeyArrowRight:
		e.quitArmed = false
		if !e.selecting {
			e.clearSelection()
		}
		e.moveCursor(key)
	case editorKeyPageUp:
		e.quitArmed = false
		if !e.selecting {
			e.clearSelection()
		}
		for i := 0; i < e.screenRows; i++ {
			e.moveCursor(editorKeyArrowUp)
		}
	case editorKeyPageDown:
		e.quitArmed = false
		if !e.selecting {
			e.clearSelection()
		}
		for i := 0; i < e.screenRows; i++ {
			e.moveCursor(editorKeyArrowDown)
		}
	case editorKeyMouseWheelUp:
		e.quitArmed = false
		e.clearSelection()
		for i := 0; i < 1; i++ {
			e.moveCursor(editorKeyArrowUp)
		}
	case editorKeyMouseWheelDown:
		e.quitArmed = false
		e.clearSelection()
		for i := 0; i < 1; i++ {
			e.moveCursor(editorKeyArrowDown)
		}
	case editorKeyHome:
		e.quitArmed = false
		if !e.selecting {
			e.clearSelection()
		}
		e.cx = 0
	case editorKeyEnd:
		e.quitArmed = false
		if !e.selecting {
			e.clearSelection()
		}
		e.cx = len([]rune(e.currentLine()))
	case editorKeyDelete:
		e.quitArmed = false
		if e.hasSelection() {
			e.beginUndoGroup()
			e.deleteSelection()
			return false, nil
		}
		e.beginUndoGroup()
		e.deleteCharForward()
	case 127, 8:
		e.quitArmed = false
		if e.hasSelection() {
			e.beginUndoGroup()
			e.deleteSelection()
			return false, nil
		}
		e.beginUndoGroup()
		e.backspace()
	case '\x1b':
		e.quitArmed = false
		if e.selecting {
			e.clearSelection()
			e.setStatusMessage("Selection cleared")
		}
	case '\r':
		e.quitArmed = false
		e.beginUndoGroup()
		if e.hasSelection() {
			e.deleteSelection()
		}
		e.insertNewline()
	case '\t':
		e.quitArmed = false
		e.beginUndoGroup()
		if e.hasSelection() {
			e.deleteSelection()
		}
		e.insertString("    ")
	default:
		if key >= 32 && key <= 126 {
			e.quitArmed = false
			e.beginUndoGroup()
			if e.hasSelection() {
				e.deleteSelection()
			}
			e.insertRune(rune(key))
		}
	}
	return false, nil
}

func (e *marEditor) currentLine() string {
	if e.cy < 0 || e.cy >= len(e.lines) {
		return ""
	}
	return e.lines[e.cy]
}

func (e *marEditor) setCurrentLine(value string) {
	if e.cy < 0 || e.cy >= len(e.lines) {
		return
	}
	e.lines[e.cy] = value
}

func (e *marEditor) insertRune(r rune) {
	line := []rune(e.currentLine())
	if e.cx < 0 {
		e.cx = 0
	}
	if e.cx > len(line) {
		e.cx = len(line)
	}
	line = append(line[:e.cx], append([]rune{r}, line[e.cx:]...)...)
	e.setCurrentLine(string(line))
	e.cx++
	e.updateDirty()
	e.updateGitSigns()
}

func (e *marEditor) insertString(value string) {
	for _, r := range value {
		e.insertRune(r)
	}
}

func (e *marEditor) insertText(value string) {
	for _, r := range value {
		if r == '\n' {
			e.insertNewline()
			continue
		}
		e.insertRune(r)
	}
}

func (e *marEditor) insertNewline() {
	line := []rune(e.currentLine())
	left := string(line[:e.cx])
	right := string(line[e.cx:])
	e.lines[e.cy] = left
	e.lines = append(e.lines[:e.cy+1], append([]string{right}, e.lines[e.cy+1:]...)...)
	e.cy++
	e.cx = 0
	e.updateDirty()
	e.updateGitSigns()
}

func (e *marEditor) backspace() {
	if e.cx == 0 {
		if e.cy == 0 {
			return
		}
		prev := e.lines[e.cy-1]
		current := e.currentLine()
		e.cx = len([]rune(prev))
		e.lines[e.cy-1] = prev + current
		e.lines = append(e.lines[:e.cy], e.lines[e.cy+1:]...)
		e.cy--
		e.updateDirty()
		e.updateGitSigns()
		return
	}
	line := []rune(e.currentLine())
	line = append(line[:e.cx-1], line[e.cx:]...)
	e.setCurrentLine(string(line))
	e.cx--
	e.updateDirty()
	e.updateGitSigns()
}

func (e *marEditor) deleteCharForward() {
	line := []rune(e.currentLine())
	if e.cx >= len(line) {
		if e.cy+1 >= len(e.lines) {
			return
		}
		e.lines[e.cy] += e.lines[e.cy+1]
		e.lines = append(e.lines[:e.cy+1], e.lines[e.cy+2:]...)
		e.updateDirty()
		e.updateGitSigns()
		return
	}
	line = append(line[:e.cx], line[e.cx+1:]...)
	e.setCurrentLine(string(line))
	e.updateDirty()
	e.updateGitSigns()
}

func (e *marEditor) moveCursor(key int) {
	switch key {
	case editorKeyArrowLeft:
		if e.cx > 0 {
			e.cx--
		} else if e.cy > 0 {
			e.cy--
			e.cx = len([]rune(e.currentLine()))
		}
	case editorKeyArrowRight:
		lineLen := len([]rune(e.currentLine()))
		if e.cx < lineLen {
			e.cx++
		} else if e.cy+1 < len(e.lines) {
			e.cy++
			e.cx = 0
		}
	case editorKeyArrowUp:
		if e.cy > 0 {
			e.cy--
		}
	case editorKeyArrowDown:
		if e.cy+1 < len(e.lines) {
			e.cy++
		}
	}
	lineLen := len([]rune(e.currentLine()))
	if e.cx > lineLen {
		e.cx = lineLen
	}
}

func (e *marEditor) beginSelection() {
	if e.selecting {
		return
	}
	e.selecting = true
	e.selectX = e.cx
	e.selectY = e.cy
}

func (e *marEditor) clearSelection() {
	e.selecting = false
}

func (e *marEditor) hasSelection() bool {
	return e.selecting && (e.selectX != e.cx || e.selectY != e.cy)
}

func (e *marEditor) selectionBounds() (editorPos, editorPos, bool) {
	if !e.hasSelection() {
		return editorPos{}, editorPos{}, false
	}
	start := editorPos{x: e.selectX, y: e.selectY}
	end := editorPos{x: e.cx, y: e.cy}
	if editorPosCompare(start, end) > 0 {
		start, end = end, start
	}
	return start, end, true
}

func editorPosCompare(a, b editorPos) int {
	if a.y < b.y {
		return -1
	}
	if a.y > b.y {
		return 1
	}
	if a.x < b.x {
		return -1
	}
	if a.x > b.x {
		return 1
	}
	return 0
}

func (e *marEditor) selectedText() string {
	start, end, ok := e.selectionBounds()
	if !ok {
		return ""
	}
	if start.y == end.y {
		line := []rune(e.lines[start.y])
		return string(line[start.x:end.x])
	}

	var parts []string
	first := []rune(e.lines[start.y])
	parts = append(parts, string(first[start.x:]))
	for row := start.y + 1; row < end.y; row++ {
		parts = append(parts, e.lines[row])
	}
	last := []rune(e.lines[end.y])
	parts = append(parts, string(last[:end.x]))
	return strings.Join(parts, "\n")
}

func (e *marEditor) deleteSelection() {
	start, end, ok := e.selectionBounds()
	if !ok {
		return
	}
	if start.y == end.y {
		line := []rune(e.lines[start.y])
		e.lines[start.y] = string(append(line[:start.x], line[end.x:]...))
	} else {
		first := []rune(e.lines[start.y])
		last := []rune(e.lines[end.y])
		merged := string(first[:start.x]) + string(last[end.x:])
		e.lines = append(e.lines[:start.y], append([]string{merged}, e.lines[end.y+1:]...)...)
	}
	e.cx = start.x
	e.cy = start.y
	e.clearSelection()
	e.updateDirty()
	e.updateGitSigns()
}

func (e *marEditor) save() error {
	data := strings.Join(e.lines, "\n") + "\n"
	formatted, formatErr := formatter.Format(data)
	toWrite := data
	if formatErr == nil {
		toWrite = formatted
		e.lines = splitEditorLines(formatted)
		lineLen := len([]rune(e.currentLine()))
		if e.cx > lineLen {
			e.cx = lineLen
		}
	}
	if err := os.WriteFile(e.filePath, []byte(toWrite), 0o644); err != nil {
		return err
	}
	e.savedLines = cloneEditorLines(e.lines)
	e.updateDirty()
	e.updateGitSigns()
	e.quitArmed = false
	if formatErr != nil {
		e.setStatusMessage(fmt.Sprintf("Saved %s (format failed: %s)", filepath.Base(e.filePath), formatErr.Error()))
		return nil
	}
	e.setStatusMessage(fmt.Sprintf("Saved %s", filepath.Base(e.filePath)))
	return nil
}

func (e *marEditor) beginUndoGroup() {
	e.undoStack = append(e.undoStack, editorSnapshot{
		lines: cloneEditorLines(e.lines),
		cx:    e.cx,
		cy:    e.cy,
	})
	e.redoStack = nil
}

func cloneEditorLines(lines []string) []string {
	cloned := make([]string, len(lines))
	copy(cloned, lines)
	return cloned
}

func (e *marEditor) restoreSnapshot(snapshot editorSnapshot) {
	e.lines = cloneEditorLines(snapshot.lines)
	e.cx = snapshot.cx
	e.cy = snapshot.cy
	e.updateDirty()
	e.clearSelection()
	e.updateGitSigns()
}

func (e *marEditor) undo() {
	if len(e.undoStack) == 0 {
		e.setStatusMessage("Nothing to undo")
		return
	}
	e.redoStack = append(e.redoStack, editorSnapshot{
		lines: cloneEditorLines(e.lines),
		cx:    e.cx,
		cy:    e.cy,
	})
	snapshot := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.restoreSnapshot(snapshot)
	e.setStatusMessage("Last change undone")
}

func (e *marEditor) redo() {
	if len(e.redoStack) == 0 {
		e.setStatusMessage("Nothing to redo")
		return
	}
	e.undoStack = append(e.undoStack, editorSnapshot{
		lines: cloneEditorLines(e.lines),
		cx:    e.cx,
		cy:    e.cy,
	})
	snapshot := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	e.restoreSnapshot(snapshot)
	e.setStatusMessage("Last change restored")
}

func (e *marEditor) setStatusMessage(message string) {
	e.status = message
	e.statusTime = time.Now()
}

func (e *marEditor) updateDirty() {
	e.dirty = !editorLinesEqual(e.lines, e.savedLines)
}

func editorLinesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *marEditor) refreshScreen() {
	rows, cols := editorTerminalSize()
	e.screenRows = rows
	e.screenCols = cols
	e.scroll()

	var out bytes.Buffer
	out.WriteString("\x1b[H")

	for y := 0; y < e.screenRows; y++ {
		fileRow := y + e.rowOffset
		if fileRow >= len(e.lines) {
			out.WriteString("\x1b[2K")
			if len(e.lines) == 1 && e.lines[0] == "" && y == e.screenRows/3 {
				welcome := "Mar editor — Ctrl-s save | Ctrl-q quit"
				if len(welcome) > e.screenCols {
					welcome = welcome[:e.screenCols]
				}
				padding := (e.screenCols - len(welcome)) / 2
				if padding > 0 {
					out.WriteString("~")
					padding--
				}
				if padding > 0 {
					out.WriteString(strings.Repeat(" ", padding))
				}
				out.WriteString(colorizeCLI(e.useColor, "\033[2m", welcome))
			} else {
				out.WriteString(colorizeCLI(e.useColor, "\033[2m", "~"))
			}
		} else {
			e.drawLine(&out, fileRow)
		}
		out.WriteString("\x1b[K")
		out.WriteString("\r\n")
	}

	e.drawStatusBar(&out)
	e.drawMessageBar(&out)

	cursorRow := e.cy - e.rowOffset + 1
	cursorCol := e.renderCursorX() - e.colOffset + e.lineNumberWidth() + e.gutterExtraWidth() + 1
	out.WriteString(fmt.Sprintf("\x1b[%d;%dH", cursorRow, cursorCol))
	out.WriteString("\x1b[?25h")
	fmt.Print(out.String())
}

func (e *marEditor) drawLine(out *bytes.Buffer, fileRow int) {
	lineNumberWidth := e.lineNumberWidth()
	lineNo := strconv.Itoa(fileRow + 1)
	padding := strings.Repeat(" ", lineNumberWidth-len(lineNo))
	out.WriteString("\x1b[2K")
	out.WriteString(colorizeCLI(e.useColor, "\033[38;5;244m", padding+lineNo))
	out.WriteString(" ")
	out.WriteString(e.gitSign(fileRow))
	out.WriteString(colorizeCLI(e.useColor, "\033[38;5;240m", " │ "))

	rendered := editorExpandTabs(e.lines[fileRow])
	visible := editorVisibleRunes(rendered, e.colOffset, max(0, e.screenCols-lineNumberWidth-e.gutterExtraWidth()))
	selectFrom, selectTo, hasSelection := e.selectionForLine(fileRow)
	out.WriteString(editorHighlightLine(visible, e.useColor, selectFrom, selectTo, hasSelection))
}

func (e *marEditor) drawStatusBar(out *bytes.Buffer) {
	out.WriteString("\x1b[7m")
	left := " " + e.statusBarLeftText()
	right := fmt.Sprintf(" %d/%d ", e.cy+1, len(e.lines))
	if len(left)+len(right) > e.screenCols {
		left = truncateString(left, max(0, e.screenCols-len(right)))
	}
	padding := e.screenCols - len(left) - len(right)
	if padding < 0 {
		padding = 0
	}
	out.WriteString(left)
	out.WriteString(strings.Repeat(" ", padding))
	out.WriteString(right)
	out.WriteString("\x1b[m\r\n")
}

func (e *marEditor) drawMessageBar(out *bytes.Buffer) {
	out.WriteString("\x1b[2K")
	help := e.helpText()
	out.WriteString(truncateString(help, e.screenCols))
}

func (e *marEditor) activeStatusMessage() string {
	if time.Since(e.statusTime) <= 10*time.Second {
		return strings.TrimSpace(e.status)
	}
	return ""
}

func (e *marEditor) statusBarLeftText() string {
	if message := e.activeStatusMessage(); message != "" {
		return message
	}

	fileName := filepath.Base(e.filePath)
	if fileName == "" {
		fileName = "[No Name]"
	}
	if e.dirty {
		return fileName + " (modified)"
	}
	return fileName
}

func (e *marEditor) helpText() string {
	var items []string
	if e.selecting {
		items = append(items, "Esc cancel selection", "Arrows extend", "Ctrl-c copy", "Ctrl-x cut")
		if e.clipboard != "" {
			items = append(items, "Ctrl-v paste")
		}
		if e.dirty {
			items = append(items, "Ctrl-s save")
		}
		if len(e.undoStack) > 0 {
			items = append(items, "Ctrl-z undo")
		}
		if len(e.redoStack) > 0 {
			items = append(items, "Ctrl-y redo")
		}
		return strings.Join(items, " | ")
	}
	items = append(items, "Ctrl-q quit", "Ctrl-Space select")
	if e.dirty {
		items = append(items, "Ctrl-s save")
	}
	if len(e.undoStack) > 0 {
		items = append(items, "Ctrl-z undo")
	}
	if len(e.redoStack) > 0 {
		items = append(items, "Ctrl-y redo")
	}
	return strings.Join(items, " | ")
}

func (e *marEditor) lineNumberWidth() int {
	width := len(strconv.Itoa(len(e.lines)))
	if width < 2 {
		return 2
	}
	return width
}

func (e *marEditor) renderCursorX() int {
	return len([]rune(editorExpandTabs(string([]rune(e.currentLine())[:min(e.cx, len([]rune(e.currentLine())))]))))
}

func (e *marEditor) gutterExtraWidth() int {
	return 5
}

func (e *marEditor) scroll() {
	renderX := e.renderCursorX()

	if e.cy < e.rowOffset {
		e.rowOffset = e.cy
	}
	if e.cy >= e.rowOffset+e.screenRows {
		e.rowOffset = e.cy - e.screenRows + 1
	}
	lineAreaWidth := max(0, e.screenCols-e.lineNumberWidth()-e.gutterExtraWidth())
	if renderX < e.colOffset {
		e.colOffset = renderX
	}
	if renderX >= e.colOffset+lineAreaWidth {
		e.colOffset = renderX - lineAreaWidth + 1
	}
}

func editorExpandTabs(line string) string {
	return strings.ReplaceAll(line, "\t", "    ")
}

func editorVisibleRunes(value string, start, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if start >= len(runes) {
		return ""
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

func (e *marEditor) selectionForLine(fileRow int) (int, int, bool) {
	start, end, ok := e.selectionBounds()
	if !ok || fileRow < start.y || fileRow > end.y {
		return 0, 0, false
	}
	line := []rune(editorExpandTabs(e.lines[fileRow]))
	from := 0
	to := len(line)
	if fileRow == start.y {
		from = len([]rune(editorExpandTabs(string([]rune(e.lines[fileRow])[:start.x]))))
	}
	if fileRow == end.y {
		to = len([]rune(editorExpandTabs(string([]rune(e.lines[fileRow])[:end.x]))))
	}
	from -= e.colOffset
	to -= e.colOffset
	if from < 0 {
		from = 0
	}
	visibleWidth := max(0, e.screenCols-e.lineNumberWidth()-e.gutterExtraWidth())
	if to > visibleWidth {
		to = visibleWidth
	}
	if from > to {
		from = to
	}
	return from, to, true
}

func (e *marEditor) gitSign(fileRow int) string {
	if e.gitSigns == nil {
		return " "
	}
	sign := e.gitSigns[fileRow]
	switch sign {
	case '+':
		return colorizeCLI(e.useColor, "\033[38;5;70m", "+")
	case '~':
		return colorizeCLI(e.useColor, "\033[38;5;179m", "~")
	case '-':
		return colorizeCLI(e.useColor, "\033[38;5;203m", "-")
	default:
		return " "
	}
}

func (e *marEditor) updateGitSigns() {
	if len(e.gitBase) == 0 {
		e.gitSigns = nil
		return
	}
	e.gitSigns = computeEditorGitSigns(e.gitBase, e.lines)
	if len(e.gitSigns) == 0 {
		e.gitSigns = nil
	}
}

func gitBaseLines(path string) []string {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(absPath)
	rootCmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	rootOut, err := rootCmd.Output()
	if err != nil {
		return nil
	}
	root := strings.TrimSpace(string(rootOut))
	if root == "" {
		return nil
	}

	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		return nil
	}
	relPath = filepath.ToSlash(relPath)

	trackedCmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", relPath)
	if err := trackedCmd.Run(); err != nil {
		return nil
	}

	showCmd := exec.Command("git", "-C", root, "show", "HEAD:"+relPath)
	content, err := showCmd.Output()
	if err != nil {
		return nil
	}
	return splitEditorLines(string(content))
}

func computeEditorGitSigns(base, current []string) map[int]rune {
	n := len(base)
	m := len(current)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if base[i] == current[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	type diffOp int
	const (
		diffEqual diffOp = iota
		diffDelete
		diffInsert
	)
	type op struct {
		kind     diffOp
		currLine int
	}

	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		if base[i] == current[j] {
			ops = append(ops, op{kind: diffEqual, currLine: j})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, op{kind: diffDelete, currLine: j})
			i++
		} else {
			ops = append(ops, op{kind: diffInsert, currLine: j})
			j++
		}
	}
	for i < n {
		ops = append(ops, op{kind: diffDelete, currLine: j})
		i++
	}
	for j < m {
		ops = append(ops, op{kind: diffInsert, currLine: j})
		j++
	}

	signs := make(map[int]rune)
	currentPos := 0
	for idx := 0; idx < len(ops); {
		if ops[idx].kind == diffEqual {
			currentPos = ops[idx].currLine + 1
			idx++
			continue
		}

		startCurrentPos := currentPos
		var inserts []int
		deletes := 0
		for idx < len(ops) && ops[idx].kind != diffEqual {
			switch ops[idx].kind {
			case diffDelete:
				deletes++
			case diffInsert:
				inserts = append(inserts, ops[idx].currLine)
				currentPos = ops[idx].currLine + 1
			}
			idx++
		}

		switch {
		case deletes > 0 && len(inserts) > 0:
			for _, line := range inserts {
				signs[line] = '~'
			}
		case deletes == 0 && len(inserts) > 0:
			for _, line := range inserts {
				signs[line] = '+'
			}
		case deletes > 0 && len(inserts) == 0:
			target := startCurrentPos
			if target >= len(current) {
				target = len(current) - 1
			}
			if target >= 0 {
				signs[target] = '-'
			}
		}
	}

	return signs
}

func editorHighlightLine(line string, useColor bool, selectFrom, selectTo int, hasSelection bool) string {
	if line == "" {
		return line
	}

	var out strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); {
		style := ""
		tokenStart := i
		if runes[i] == '"' {
			j := i + 1
			for j < len(runes) {
				if runes[j] == '"' && runes[j-1] != '\\' {
					j++
					break
				}
				j++
			}
			style = "\033[38;5;114m"
			out.WriteString(editorColorizeSegment(useColor, string(runes[i:j]), style, tokenStart, selectFrom, selectTo, hasSelection))
			i = j
			continue
		}
		if unicode.IsDigit(runes[i]) {
			j := i + 1
			for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.') {
				j++
			}
			out.WriteString(editorColorizeSegment(useColor, string(runes[i:j]), "\033[38;5;179m", tokenStart, selectFrom, selectTo, hasSelection))
			i = j
			continue
		}
		if unicode.IsLetter(runes[i]) || runes[i] == '_' {
			j := i + 1
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_' || runes[j] == '.') {
				j++
			}
			token := string(runes[i:j])
			k := j
			for k < len(runes) && unicode.IsSpace(runes[k]) {
				k++
			}
			declaresAlias := k < len(runes) && runes[k] == '='
			switch {
			case token == "input" || strings.HasPrefix(token, "input.") || expr.IsBuiltinValueName(token):
				style = "\033[38;5;81m"
			case declaresAlias:
				style = "\033[38;5;81m"
			case strings.Contains(token, "."):
				style = "\033[38;5;81m"
			case tokenInSet(token, marEditorKeywords):
				style = "\033[38;5;75m"
			case tokenInSet(token, marEditorFunctions):
				style = "\033[38;5;114m"
			case tokenInSet(token, marEditorLogicalOperators):
				style = "\033[38;5;110m"
			case tokenInSet(token, marEditorTypes):
				style = "\033[38;5;141m"
			case len(token) > 0 && unicode.IsUpper(rune(token[0])):
				style = "\033[38;5;141m"
			case tokenInSet(token, marEditorFieldModifiers):
				style = "\033[38;5;110m"
			case tokenInSet(token, marEditorLiterals):
				style = "\033[38;5;179m"
			default:
				style = ""
			}
			out.WriteString(editorColorizeSegment(useColor, token, style, tokenStart, selectFrom, selectTo, hasSelection))
			i = j
			continue
		}
		if i+1 < len(runes) && runes[i] == '-' && runes[i+1] == '-' {
			style = "\033[38;5;244m"
			out.WriteString(editorColorizeSegment(useColor, string(runes[i:]), style, tokenStart, selectFrom, selectTo, hasSelection))
			break
		}
		out.WriteString(editorColorizeSegment(useColor, string(runes[i]), "", tokenStart, selectFrom, selectTo, hasSelection))
		i++
	}
	return out.String()
}

func editorColorizeSegment(useColor bool, segment, style string, tokenStart, selectFrom, selectTo int, hasSelection bool) string {
	if !useColor {
		return segment
	}

	runes := []rune(segment)
	var out strings.Builder
	for idx, r := range runes {
		inSelection := hasSelection && tokenStart+idx >= selectFrom && tokenStart+idx < selectTo
		switch {
		case inSelection && style != "":
			out.WriteString("\033[7m")
			out.WriteString(style)
			out.WriteRune(r)
			out.WriteString("\033[0m")
		case inSelection:
			out.WriteString("\033[7m")
			out.WriteRune(r)
			out.WriteString("\033[0m")
		case style != "":
			out.WriteString(style)
			out.WriteRune(r)
			out.WriteString("\033[0m")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func tokenInSet(token string, values map[string]struct{}) bool {
	_, ok := values[token]
	return ok
}

func truncateString(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:width])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
