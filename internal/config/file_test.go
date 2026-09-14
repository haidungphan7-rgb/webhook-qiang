package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeEnv builds the getenv function defaultPath takes. Injecting the environment is what
// lets one test cover all three platforms on any host - t.Setenv cannot change
// runtime.GOOS, and the real lookup cannot be stubbed otherwise.
func fakeEnv(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()

	const (
		homeLinux    = "/home/alice"
		homeDarwin   = "/Users/alice"
		localAppData = `C:\Users\alice\AppData\Local`
		roamingData  = `C:\Users\alice\AppData\Roaming`
	)

	cases := []struct {
		name    string
		goos    string
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "linux honours XDG_CONFIG_HOME",
			goos: "linux",
			env:  map[string]string{"HOME": homeLinux, "XDG_CONFIG_HOME": "/home/alice/custom-config"},
			want: filepath.Join("/home/alice/custom-config", "webhook-zq", "config.json"),
		},
		{
			name: "linux falls back to ~/.config",
			goos: "linux",
			env:  map[string]string{"HOME": homeLinux},
			want: filepath.Join(homeLinux, ".config", "webhook-zq", "config.json"),
		},
		{
			name:    "linux rejects a relative XDG_CONFIG_HOME",
			goos:    "linux",
			env:     map[string]string{"HOME": homeLinux, "XDG_CONFIG_HOME": "relative/config"},
			wantErr: "relative",
		},
		{
			name:    "linux without HOME and without XDG_CONFIG_HOME",
			goos:    "linux",
			env:     map[string]string{},
			wantErr: "neither $XDG_CONFIG_HOME nor $HOME",
		},
		{
			name: "macos uses ~/Library/Application Support",
			goos: "darwin",
			env:  map[string]string{"HOME": homeDarwin},
			want: filepath.Join(homeDarwin, "Library", "Application Support", "webhook-zq", "config.json"),
		},
		{
			name:    "macos without HOME",
			goos:    "darwin",
			env:     map[string]string{},
			wantErr: "$HOME is not defined",
		},
		{
			// The one place this differs from os.UserConfigDir(), and the reason the
			// function exists: a file that can hold a password must not roam.
			name: "windows prefers LOCALAPPDATA over the roaming profile",
			goos: "windows",
			env:  map[string]string{"LOCALAPPDATA": localAppData, "APPDATA": roamingData},
			want: filepath.Join(localAppData, "webhook-zq", "config.json"),
		},
		{
			name: "windows falls back to APPDATA",
			goos: "windows",
			env:  map[string]string{"APPDATA": roamingData},
			want: filepath.Join(roamingData, "webhook-zq", "config.json"),
		},
		{
			name:    "windows without either variable",
			goos:    "windows",
			env:     map[string]string{},
			wantErr: "%LOCALAPPDATA% nor %APPDATA%",
		},
		{
			name: "WEBHOOK_ZQ_CONFIG wins on every platform",
			goos: "windows",
			env:  map[string]string{"WEBHOOK_ZQ_CONFIG": "/explicit/path.json", "LOCALAPPDATA": localAppData},
			want: "/explicit/path.json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := defaultPath(tc.goos, fakeEnv(tc.env))

			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestApply(t *testing.T) {
	t.Parallel()

	d := func(s string) time.Duration {
		v, err := time.ParseDuration(s)
		require.NoError(t, err)

		return v
	}

	file := &File{
		ReplayTimeout:      "45s",
		ReplayBackoff:      "3s",
		ReplayMaxPreview:   4096,
		ReplayMaxRedirects: 7,
		ReplayMaxRetries:   4,
		ReplayRateLimit:    60,
		ReplayAllowHosts:   []string{"example.com"},
		ReplayAllowPrivate: true,
		RetentionMaxEvents: 5000,
		RetentionMaxDays:   14,
		RetentionInterval:  "2h",
		MaxRequestBodySize: 1 << 20,
		PublicURLRoot:      "https://example.com",
		TrustProxy:         true,
	}

	t.Run("nil file leaves the settings alone", func(t *testing.T) {
		t.Parallel()

		s := AppSettings{ReplayTimeout: d("10s")}
		Apply(&s, nil, func(string) bool { return false })
		require.Equal(t, d("10s"), s.ReplayTimeout)
	})

	t.Run("file fills what was not provided", func(t *testing.T) {
		t.Parallel()

		s := AppSettings{}
		Apply(&s, file, func(string) bool { return false })
		require.Equal(t, d("45s"), s.ReplayTimeout)
		require.Equal(t, d("3s"), s.ReplayBackoff)
		require.Equal(t, 4096, s.ReplayMaxPreview)
		require.Equal(t, 7, s.ReplayMaxRedirects)
		require.Equal(t, 4, s.ReplayMaxRetries)
		require.Equal(t, 60, s.ReplayRateLimit)
		require.Equal(t, []string{"example.com"}, s.ReplayAllowHosts)
		require.True(t, s.ReplayAllowPrivate)
		require.Equal(t, 5000, s.RetentionMaxEvents)
		require.Equal(t, 14, s.RetentionMaxDays)
		require.Equal(t, d("2h"), s.RetentionInterval)
		require.Equal(t, uint32(1<<20), s.MaxRequestBodySize)
		require.Equal(t, "https://example.com", s.PublicURLRoot)
		require.True(t, s.TrustProxy)
	})

	t.Run("provided values win over the file", func(t *testing.T) {
		t.Parallel()

		s := AppSettings{ReplayTimeout: d("10s")}
		Apply(&s, file, func(key string) bool { return key == "replay-timeout" })
		require.Equal(t, d("10s"), s.ReplayTimeout)
		// every other field still comes from the file
		require.Equal(t, d("3s"), s.ReplayBackoff)
	})

	t.Run("unparsable durations in the file are ignored", func(t *testing.T) {
		t.Parallel()

		s := AppSettings{ReplayTimeout: d("10s")}
		Apply(&s, &File{ReplayTimeout: "not-a-duration"}, func(string) bool { return false })
		require.Equal(t, d("10s"), s.ReplayTimeout)
	})
}

func TestApplyListen(t *testing.T) {
	t.Parallel()

	t.Run("nil file leaves the pair alone", func(t *testing.T) {
		t.Parallel()

		addr, port := ApplyListen("127.0.0.1", 8080, nil, func(string) bool { return false })
		require.Equal(t, "127.0.0.1", addr)
		require.Equal(t, uint16(8080), port)
	})

	t.Run("file fills the pair", func(t *testing.T) {
		t.Parallel()

		addr, port := ApplyListen("", 0, &File{Addr: "127.0.0.1", Port: 9090}, func(string) bool { return false })
		require.Equal(t, "127.0.0.1", addr)
		require.Equal(t, uint16(9090), port)
	})

	t.Run("provided pair wins over the file", func(t *testing.T) {
		t.Parallel()

		addr, port := ApplyListen("0.0.0.0", 8081, &File{Addr: "127.0.0.1", Port: 9090},
			func(key string) bool { return key == "addr" || key == "port" })
		require.Equal(t, "0.0.0.0", addr)
		require.Equal(t, uint16(8081), port)
	})

	t.Run("one side provided, one side from the file", func(t *testing.T) {
		t.Parallel()

		addr, port := ApplyListen("0.0.0.0", 0, &File{Addr: "127.0.0.1", Port: 9090},
			func(key string) bool { return key == "addr" })
		require.Equal(t, "0.0.0.0", addr)
		require.Equal(t, uint16(9090), port)
	})
}
