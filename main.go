package main

import (
	"bufio"
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/db"
	"github.com/hcchien/reviz-accounting/internal/handlers"
	filestore "github.com/hcchien/reviz-accounting/internal/storage"
)

//go:embed web/templates/*.html
var templatesFS embed.FS

//go:embed all:web/static
var staticFS embed.FS

//go:embed web/static/template/simpany-v0.4.0.xlsx
var simpanyTemplate []byte

func main() {
	defaultAddr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		defaultAddr = ":" + p
	}
	var (
		addr              = flag.String("addr", defaultAddr, "HTTP listen address (overrides $PORT)")
		dbURL             = flag.String("db-url", "", "PostgreSQL connection URL (or $DATABASE_URL)")
		createUser        = flag.String("create-user", "", "Create a user with this username (prompts for password) and exit")
		createRole        = flag.String("create-role", "owner", "Role for -create-user (owner|accountant|viewer)")
		createPasswordEnv = flag.String("create-password-env", "", "Environment variable containing password for non-interactive -create-user")
		attachmentsDir    = flag.String("attachments-dir", "data/attachments", "Local attachment directory (used when $GCS_BUCKET is unset)")
	)
	flag.Parse()
	if *dbURL == "" {
		*dbURL = os.Getenv("DATABASE_URL")
	}

	d, err := db.Open(*dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer d.Close()

	if err := db.SeedIfEmpty(d); err != nil {
		log.Fatalf("seed: %v", err)
	}

	if *createUser != "" {
		var pw string
		if *createPasswordEnv != "" {
			pw = os.Getenv(*createPasswordEnv)
			if pw == "" {
				log.Fatalf("環境變數 %s 沒有密碼", *createPasswordEnv)
			}
		} else {
			pw, err = readPasswordInteractive("輸入密碼 (≥ 6 字元): ")
			if err != nil {
				log.Fatalf("讀取密碼失敗: %v", err)
			}
			confirm, err := readPasswordInteractive("再次輸入密碼: ")
			if err != nil {
				log.Fatalf("讀取密碼失敗: %v", err)
			}
			if pw != confirm {
				log.Fatal("兩次密碼不一致")
			}
		}
		u, err := auth.CreateUser(d, *createUser, pw, *createRole)
		if err != nil {
			log.Fatalf("建立使用者失敗: %v", err)
		}
		fmt.Printf("✓ 已建立 %s (role=%s, id=%d)\n", u.Username, u.Role, u.ID)
		return
	}

	if n, _ := auth.CountUsers(d); n == 0 {
		log.Println("⚠️  尚未建立任何使用者。請先執行：")
		log.Println("    ./reviz-accounting -create-user <帳號>")
		log.Println("    （預設角色 owner，可用 -create-role accountant|viewer 更改）")
	}

	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for range t.C {
			_ = auth.PurgeExpiredSessions(d)
		}
	}()

	attachmentStore, err := filestore.New(context.Background(), os.Getenv("GCS_BUCKET"), *attachmentsDir)
	if err != nil {
		log.Fatalf("init attachment storage: %v", err)
	}
	defer attachmentStore.Close()
	srv, err := handlers.NewServer(d, templatesFS, attachmentStore)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}
	srv.SimpanyTemplate = simpanyTemplate

	mux := http.NewServeMux()
	srv.Routes(mux)

	// Static assets (CSS, fonts, images) — embedded into the binary.
	staticSub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		log.Fatalf("static sub-fs: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	handler := withLogging(auth.Attach(d, mux))

	log.Printf("reviz-accounting listening on http://localhost%s (PostgreSQL)", *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func withLogging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

// stdinReader is shared across calls so bufio's read-ahead buffer is not
// discarded between password prompts when stdin is piped.
var stdinReader = bufio.NewReader(os.Stdin)

// readPasswordInteractive reads a password without echo when running in a
// terminal; otherwise it reads a line from stdin (used for piped input).
func readPasswordInteractive(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
