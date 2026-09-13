package config

import (
	"path/filepath"
	"testing"

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
