// Command factory is a pixel-art tower defense game for Ghostty (and any terminal).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

const version = "0.2.0"

func home() string {
	if d := os.Getenv("FACTORY_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".factory")
}

func main() {
	args := os.Args[1:]
	forceBlocks := false
	for i, a := range args {
		switch a {
		case "msg":
			cmdMsg(args[i+1:])
			return
		case "hook":
			cmdHook(os.Stdin)
			return
		case "--version", "-v":
			fmt.Println("factory", version)
			return
		case "--help", "-h", "help":
			fmt.Print(usage)
			return
		case "--blocks":
			forceBlocks = true
		default:
			fmt.Fprintf(os.Stderr, "factory: unknown argument %q\n%s", a, usage)
			os.Exit(2)
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "factory: needs a terminal")
		os.Exit(1)
	}
	if err := run(forceBlocks); err != nil {
		fmt.Fprintln(os.Stderr, "factory:", err)
		os.Exit(1)
	}
}

const usage = `factory - defend your factory from waves of scrap bots (best in Ghostty)

  factory              play a match against the rival AI
  factory --blocks     force the text renderer instead of pixel graphics
  factory msg [--from NAME] TEXT...   send a message into the game
  factory hook         Claude Code hook: reads hook JSON on stdin
`

// --- agent messages ---

type Msg struct{ From, Text string }

func inbox() string { return filepath.Join(home(), "inbox") }

func writeMsg(from, text string) error {
	if err := os.MkdirAll(inbox(), 0o755); err != nil {
		return err
	}
	text = strings.ReplaceAll(text, `\n`, "\n")
	name := filepath.Join(inbox(), strconv.FormatInt(time.Now().UnixNano(), 10)+".md")
	return os.WriteFile(name, []byte("From: "+from+"\n"+text), 0o644)
}

func cwdName() string {
	wd, _ := os.Getwd()
	return filepath.Base(wd)
}

func cmdMsg(args []string) {
	from := cwdName()
	if len(args) >= 2 && args[0] == "--from" {
		from, args = args[1], args[2:]
	}
	text := strings.Join(args, " ")
	if text == "" {
		b, _ := io.ReadAll(os.Stdin)
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "factory msg: nothing to send")
		os.Exit(2)
	}
	if err := writeMsg(from, text); err != nil {
		fmt.Fprintln(os.Stderr, "factory msg:", err)
		os.Exit(1)
	}
}

// cmdHook turns a Claude Code Stop/Notification hook into a message. It never
// fails loudly: a broken hook must not get in the agent's way.
func cmdHook(r io.Reader) {
	var h struct {
		Event   string `json:"hook_event_name"`
		Cwd     string `json:"cwd"`
		Last    string `json:"last_assistant_message"`
		Message string `json:"message"`
	}
	if json.NewDecoder(r).Decode(&h) != nil {
		return
	}
	from := filepath.Base(h.Cwd)
	if h.Cwd == "" {
		from = "claude"
	}
	switch h.Event {
	case "Stop":
		t := h.Last
		if t == "" {
			t = "finished"
		}
		writeMsg(from, trunc(t, 500))
	case "Notification":
		writeMsg(from, "needs you: "+trunc(h.Message, 300))
	}
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func readInbox() []Msg {
	files, _ := filepath.Glob(filepath.Join(inbox(), "*"))
	sort.Strings(files)
	var out []Msg
	for _, f := range files {
		b, err := os.ReadFile(f)
		os.Remove(f)
		if err != nil || len(b) == 0 {
			continue
		}
		m := Msg{From: "inbox", Text: string(b)}
		if rest, ok := strings.CutPrefix(m.Text, "From: "); ok {
			m.From, m.Text, _ = strings.Cut(rest, "\n")
		}
		out = append(out, m)
	}
	return out
}
