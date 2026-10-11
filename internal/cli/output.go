package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// printJSON prints v as indented JSON.
func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// printRawJSON pretty-prints a JSON body, or prints it verbatim if it is not JSON.
func (a *app) printRawJSON(body []byte) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		buf.Reset()
		buf.Write(body)
	}
	buf.WriteByte('\n')
	_, err := a.stdout.Write(buf.Bytes())
	return err
}

// printTable prints rows under headers, aligned with tabwriter.
func (a *app) printTable(headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			if c == "" {
				c = "-"
			}
			cells[i] = strings.ReplaceAll(c, "\t", " ")
		}
		_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// printFields prints "Key: value" pairs, aligned.
func (a *app) printFields(pairs ...[2]string) error {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	for _, p := range pairs {
		v := p[1]
		if v == "" {
			v = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s:\t%s\n", p[0], v)
	}
	return tw.Flush()
}

// fmtTime formats a Logto millisecond timestamp.
func fmtTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func fmtBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// isTerminal reports whether r is an interactive character device.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// confirm asks a yes/no question on stdin. Without a terminal it refuses, so
// scripts must pass --yes.
func (a *app) confirm(question string) (bool, error) {
	if !isTerminal(a.stdin) {
		return false, errors.New("refusing to continue without confirmation: pass --yes")
	}
	_, _ = fmt.Fprintf(a.stderr, "%s [y/N]: ", question)
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// readSecretLine reads the first line of stdin (for --password-stdin).
func (a *app) readSecretLine() (string, error) {
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("no password on stdin")
	}
	return line, nil
}
