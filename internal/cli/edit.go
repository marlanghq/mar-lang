package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

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
)

type marEditor struct {
	filePath   string
	lines      []string
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
}

var (
	marEditorKeywords = map[string]struct{}{
		"app": {}, "port": {}, "database": {}, "public": {}, "system": {}, "auth": {}, "entity": {}, "action": {}, "type": {}, "alias": {},
		"rule": {}, "expect": {}, "authorize": {}, "when": {}, "all": {}, "list": {}, "get": {}, "create": {}, "update": {}, "delete": {},
	}
	marEditorTypes = map[string]struct{}{
		"String": {}, "Int": {}, "Bool": {}, "Float": {},
	}
	marEditorFieldModifiers = map[string]struct{}{
		"primary": {}, "auto": {}, "optional": {},
	}
	marEditorBooleans = map[string]struct{}{
		"true": {}, "false": {},
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
	fmt.Fprintf(&b, "  Open a Mar file in the terminal with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s edit todo.mar", binaryName)))
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
			"Use a file name like todo.mar.",
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
	return &marEditor{
		filePath:   trimmed,
		lines:      lines,
		screenRows: rows,
		screenCols: cols,
		useColor:   cliSupportsANSIStream(os.Stdout),
		status:     "Ctrl-S save | Ctrl-Q quit | Arrows move",
		statusTime: time.Now(),
	}, nil
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
	oldState, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
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
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &newState); err != nil {
		return nil, err
	}
	return oldState, nil
}

func editorDisableRawMode(oldState *unix.Termios) {
	if oldState == nil {
		return
	}
	_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TIOCSETA, oldState)
}

func editorReadKey() (int, error) {
	b, err := editorReadByte()
	if err != nil {
		return 0, err
	}
	if b != '\x1b' {
		return int(b), nil
	}

	b1, err := editorReadByte()
	if err != nil {
		return int('\x1b'), nil
	}
	if b1 == '[' {
		b2, err := editorReadByte()
		if err != nil {
			return int('\x1b'), nil
		}
		if b2 == '<' {
			return editorReadMouseEvent()
		}
		if b2 >= '0' && b2 <= '9' {
			b3, err := editorReadByte()
			if err != nil {
				return int('\x1b'), nil
			}
			if b3 == '~' {
				switch b2 {
				case '1', '7':
					return editorKeyHome, nil
				case '3':
					return editorKeyDelete, nil
				case '4', '8':
					return editorKeyEnd, nil
				case '5':
					return editorKeyPageUp, nil
				case '6':
					return editorKeyPageDown, nil
				}
			}
			return int('\x1b'), nil
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
		b2, err := editorReadByte()
		if err != nil {
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
	case editorCtrlKey('q'):
		if e.dirty && !e.quitArmed {
			e.quitArmed = true
			e.setStatusMessage("Unsaved changes. Press Ctrl-Q again to quit.")
			return false, nil
		}
		return true, nil
	case editorCtrlKey('s'):
		if err := e.save(); err != nil {
			e.setStatusMessage("Save failed: " + err.Error())
			return false, nil
		}
		return false, nil
	case editorKeyArrowUp, editorKeyArrowDown, editorKeyArrowLeft, editorKeyArrowRight:
		e.quitArmed = false
		e.moveCursor(key)
	case editorKeyPageUp:
		e.quitArmed = false
		for i := 0; i < e.screenRows; i++ {
			e.moveCursor(editorKeyArrowUp)
		}
	case editorKeyPageDown:
		e.quitArmed = false
		for i := 0; i < e.screenRows; i++ {
			e.moveCursor(editorKeyArrowDown)
		}
	case editorKeyMouseWheelUp:
		e.quitArmed = false
		for i := 0; i < 1; i++ {
			e.moveCursor(editorKeyArrowUp)
		}
	case editorKeyMouseWheelDown:
		e.quitArmed = false
		for i := 0; i < 1; i++ {
			e.moveCursor(editorKeyArrowDown)
		}
	case editorKeyHome:
		e.quitArmed = false
		e.cx = 0
	case editorKeyEnd:
		e.quitArmed = false
		e.cx = len([]rune(e.currentLine()))
	case editorKeyDelete:
		e.quitArmed = false
		e.deleteCharForward()
	case 127, 8:
		e.quitArmed = false
		e.backspace()
	case '\r':
		e.quitArmed = false
		e.insertNewline()
	case '\t':
		e.quitArmed = false
		e.insertString("    ")
	default:
		if key >= 32 && key <= 126 {
			e.quitArmed = false
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
	e.dirty = true
}

func (e *marEditor) insertString(value string) {
	for _, r := range value {
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
	e.dirty = true
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
		e.dirty = true
		return
	}
	line := []rune(e.currentLine())
	line = append(line[:e.cx-1], line[e.cx:]...)
	e.setCurrentLine(string(line))
	e.cx--
	e.dirty = true
}

func (e *marEditor) deleteCharForward() {
	line := []rune(e.currentLine())
	if e.cx >= len(line) {
		if e.cy+1 >= len(e.lines) {
			return
		}
		e.lines[e.cy] += e.lines[e.cy+1]
		e.lines = append(e.lines[:e.cy+1], e.lines[e.cy+2:]...)
		e.dirty = true
		return
	}
	line = append(line[:e.cx], line[e.cx+1:]...)
	e.setCurrentLine(string(line))
	e.dirty = true
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

func (e *marEditor) save() error {
	data := strings.Join(e.lines, "\n") + "\n"
	if err := os.WriteFile(e.filePath, []byte(data), 0o644); err != nil {
		return err
	}
	e.dirty = false
	e.quitArmed = false
	e.setStatusMessage(fmt.Sprintf("Saved %s", filepath.Base(e.filePath)))
	return nil
}

func (e *marEditor) setStatusMessage(message string) {
	e.status = message
	e.statusTime = time.Now()
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
				welcome := "Mar editor — Ctrl-S save | Ctrl-Q quit"
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
	cursorCol := e.renderCursorX() - e.colOffset + e.lineNumberWidth() + 4
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
	out.WriteString(colorizeCLI(e.useColor, "\033[38;5;240m", " │ "))

	rendered := editorExpandTabs(e.lines[fileRow])
	visible := editorVisibleRunes(rendered, e.colOffset, max(0, e.screenCols-lineNumberWidth-3))
	out.WriteString(editorHighlightLine(visible, e.useColor))
}

func (e *marEditor) drawStatusBar(out *bytes.Buffer) {
	out.WriteString("\x1b[7m")
	fileName := filepath.Base(e.filePath)
	if fileName == "" {
		fileName = "[No Name]"
	}
	modified := ""
	if e.dirty {
		modified = " (modified)"
	}
	left := fmt.Sprintf(" %s%s", fileName, modified)
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
	message := e.status
	if time.Since(e.statusTime) > 10*time.Second {
		message = ""
	}
	out.WriteString(truncateString(message, e.screenCols))
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

func (e *marEditor) scroll() {
	renderX := e.renderCursorX()

	if e.cy < e.rowOffset {
		e.rowOffset = e.cy
	}
	if e.cy >= e.rowOffset+e.screenRows {
		e.rowOffset = e.cy - e.screenRows + 1
	}
	lineAreaWidth := max(0, e.screenCols-e.lineNumberWidth()-3)
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

func editorHighlightLine(line string, useColor bool) string {
	if line == "" || !useColor {
		return line
	}

	var out strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); {
		if i+1 < len(runes) && runes[i] == '-' && runes[i+1] == '-' {
			out.WriteString(colorizeCLI(true, "\033[38;5;244m", string(runes[i:])))
			break
		}
		if runes[i] == '"' {
			j := i + 1
			for j < len(runes) {
				if runes[j] == '"' && runes[j-1] != '\\' {
					j++
					break
				}
				j++
			}
			out.WriteString(colorizeCLI(true, "\033[38;5;114m", string(runes[i:j])))
			i = j
			continue
		}
		if unicode.IsDigit(runes[i]) {
			j := i + 1
			for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.') {
				j++
			}
			out.WriteString(colorizeCLI(true, "\033[38;5;179m", string(runes[i:j])))
			i = j
			continue
		}
		if unicode.IsLetter(runes[i]) || runes[i] == '_' {
			j := i + 1
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_' || runes[j] == '.') {
				j++
			}
			token := string(runes[i:j])
			switch {
			case token == "input" || strings.HasPrefix(token, "input.") || strings.HasPrefix(token, "auth_"):
				out.WriteString(colorizeCLI(true, "\033[38;5;81m", token))
			case tokenInSet(token, marEditorKeywords):
				out.WriteString(colorizeCLI(true, "\033[38;5;75m", token))
			case tokenInSet(token, marEditorTypes):
				out.WriteString(colorizeCLI(true, "\033[38;5;141m", token))
			case tokenInSet(token, marEditorFieldModifiers):
				out.WriteString(colorizeCLI(true, "\033[38;5;110m", token))
			case tokenInSet(token, marEditorBooleans):
				out.WriteString(colorizeCLI(true, "\033[38;5;179m", token))
			default:
				out.WriteString(token)
			}
			i = j
			continue
		}
		out.WriteRune(runes[i])
		i++
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
