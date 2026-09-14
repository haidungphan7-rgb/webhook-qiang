package uninstallcmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestMaintenanceDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dsn     string
		wantDB  string
		wantHas string // substring the rewritten DSN must contain
	}{
		{
			name:    "url form",
			dsn:     "postgres://postgres@127.0.0.1:5432/webhook_rd?sslmode=disable",
			wantDB:  "webhook_rd",
			wantHas: "127.0.0.1:5432/postgres",
		},
		{
			name:    "url form without a database",
			dsn:     "postgres://user@localhost:5432",
			wantDB:  "webhook_rd",
			wantHas: "localhost:5432/postgres",
		},
		{
			name:    "keyword form",
			dsn:     "host=127.0.0.1 port=5432 user=postgres dbname=webhook_rd sslmode=disable",
			wantDB:  "webhook_rd",
			wantHas: "dbname=postgres",
		},
		{
			name:    "keyword form without dbname",
			dsn:     "host=localhost user=postgres",
			wantDB:  "webhook_rd",
			wantHas: "dbname=postgres",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, maint, err := maintenanceDSN(tc.dsn)
			if err != nil {
				t.Fatalf("maintenanceDSN(%q) failed: %v", tc.dsn, err)
			}

			if db != tc.wantDB {
				t.Errorf("database = %q, want %q", db, tc.wantDB)
			}

			if !strings.Contains(maint, tc.wantHas) {
				t.Errorf("maintenance DSN = %q, want it to contain %q", maint, tc.wantHas)
			}

			if strings.Contains(maint, "webhook_rd") {
				t.Errorf("maintenance DSN = %q still points at the app database", maint)
			}
		})
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		defYes  bool
		want    bool
		wantErr bool
	}{
		{name: "empty line takes the default yes", input: "\n", defYes: true, want: true},
		{name: "empty line takes the default no", input: "\n", defYes: false, want: false},
		{name: "y", input: "y\n", defYes: false, want: true},
		{name: "yes", input: "yes\n", defYes: false, want: true},
		{name: "Y upper case", input: "Y\n", defYes: false, want: true},
		{name: "n", input: "n\n", defYes: true, want: false},
		{name: "garbage means no", input: "banana\n", defYes: true, want: false},
		{name: "eof is an error, not a default", input: "", defYes: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			got, err := newPrompts(strings.NewReader(tc.input), &out).confirm("问题？", tc.defYes)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error on EOF, got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("confirm failed: %v", err)
			}

			if got != tc.want {
				t.Errorf("confirm = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAskWord(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	ok, err := newPrompts(strings.NewReader("yes\n"), &out).askWord("请输入 yes", "yes")
	if err != nil || !ok {
		t.Errorf("askWord(yes) = %v, %v; want true, nil", ok, err)
	}

	out.Reset()

	ok, err = newPrompts(strings.NewReader("no\n"), &out).askWord("请输入 yes", "yes")
	if err != nil || ok {
		t.Errorf("askWord(no) = %v, %v; want false, nil", ok, err)
	}
}

// TestPromptsShareOneReader is the regression test for piped input: both
// answers must survive one buffered read. A reader-per-line implementation
// fails exactly here - the first ReadString buffers "y\nyes\n" whole, the
// second question then starts a fresh reader that reads EOF.
func TestPromptsShareOneReader(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	p := newPrompts(strings.NewReader("y\nyes\n"), &out)

	ok, err := p.confirm("以上内容将被移除，继续？", true)
	if err != nil || !ok {
		t.Fatalf("confirm = %v, %v; want true, nil", ok, err)
	}

	ok, err = p.askWord("请输入 yes 确认删除数据库", "yes")
	if err != nil || !ok {
		t.Fatalf("askWord = %v, %v; want true, nil - piped second gate lost its input", ok, err)
	}
}

func TestValidIdent(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"webhook_rd", "a", "_x", "CamelCase123"} {
		if !validIdent(s) {
			t.Errorf("validIdent(%q) = false, want true", s)
		}
	}

	for _, s := range []string{"", "1abc", "pg-catalog", "db; DROP TABLE x", "has space"} {
		if validIdent(s) {
			t.Errorf("validIdent(%q) = true, want false", s)
		}
	}
}

func TestParsePSProcessList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want []uint32
	}{
		{name: "empty output", data: "", want: nil},
		{name: "null for no rows", data: "null", want: nil},
		{name: "garbage is ignored, not fatal", data: "not json", want: nil},
		{
			name: "single row comes back as a bare object",
			data: `{"ProcessId":101,"CommandLine":"pwsh -File D:\\webhook-zq\\scripts\\tray.ps1"}`,
			want: []uint32{101},
		},
		{
			name: "several rows come back as an array",
			data: `[{"ProcessId":1,"CommandLine":"a"},{"ProcessId":2},{"ProcessId":3,"CommandLine":null}]`,
			want: []uint32{1, 2, 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows := parsePSProcessList([]byte(tc.data))
			if len(rows) != len(tc.want) {
				t.Fatalf("parsePSProcessList(%q) returned %d rows, want %d", tc.data, len(rows), len(tc.want))
			}

			for i, pid := range tc.want {
				if rows[i].ProcessID != pid {
					t.Errorf("row %d pid = %d, want %d", i, rows[i].ProcessID, pid)
				}
			}
		})
	}
}

func TestIsTrayCommandLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{
			name:    "tray script in our repo",
			cmdline: `pwsh.exe -File D:\webhook-zq\scripts\tray.ps1`,
			want:    true,
		},
		{
			name:    "server manager via powershell",
			cmdline: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "D:\webhook-zq\scripts\server-manager.ps1"`,
			want:    true,
		},
		{
			name:    "start gui",
			cmdline: `powershell.exe -File D:/webhook-zq/scripts/start-gui.ps1`,
			want:    true,
		},
		{
			name:    "upper case repo name and script",
			cmdline: `pwsh.exe -File D:\WEBHOOK-ZQ\scripts\TRAY.PS1`,
			want:    true,
		},
		{
			name:    "somebody else's tray.ps1 is left alone",
			cmdline: `pwsh.exe -File D:\other-project\scripts\tray.ps1`,
			want:    false,
		},
		{
			name:    "our repo but an unrelated script",
			cmdline: `pwsh.exe -File D:\webhook-zq\scripts\migrate.ps1`,
			want:    false,
		},
		{
			name:    "unrelated powershell session",
			cmdline: `pwsh.exe -Command Get-ChildItem`,
			want:    false,
		},
		{
			name:    "empty",
			cmdline: ``,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isTrayCommandLine(tc.cmdline); got != tc.want {
				t.Errorf("isTrayCommandLine(%q) = %v, want %v", tc.cmdline, got, tc.want)
			}
		})
	}
}
