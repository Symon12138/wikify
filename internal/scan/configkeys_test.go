package scan

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestIsConfigInventoryFile(t *testing.T) {
	yes := []string{
		"application.yml", "src/main/resources/application.yaml", "application-dev.yml",
		"bootstrap.properties", "config/application.prod.yml", ".env", ".env.example",
		"config.json", "config.toml", "config.ini",
	}
	no := []string{
		"main.go", "package.json", "settings.yml", "application_controller.rb",
		"docker-compose.yml", "envrc", "myconfig.json",
	}
	for _, p := range yes {
		if !IsConfigInventoryFile(p) {
			t.Fatalf("expected config file: %s", p)
		}
	}
	for _, p := range no {
		if IsConfigInventoryFile(p) {
			t.Fatalf("unexpected config file: %s", p)
		}
	}
}

func TestExtractConfigKeysPerFormat(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		text string
		want []string
	}{
		{
			name: "yaml",
			rel:  "application.yml",
			text: "spring:\n  datasource:\n    url: jdbc:h2\n  redis:\n    host: localhost\nserver:\n  port: 8080\n# comment\n",
			want: []string{"spring", "spring.datasource", "spring.redis", "server", "server.port"},
		},
		{
			name: "properties",
			rel:  "application.properties",
			text: "spring.datasource.url=jdbc:h2\nserver.port=8080\n# comment\napp.name=demo\n",
			want: []string{"spring.datasource", "server.port", "app.name"},
		},
		{
			name: "env",
			rel:  ".env",
			text: "export DB_HOST=localhost\nDB_PORT=5432\n# secret\n",
			want: []string{"DB_HOST", "DB_PORT"},
		},
		{
			name: "json",
			rel:  "config.json",
			text: `{"name":"x","server":{"port":1,"host":"h"},"tags":["a","b"]}`,
			want: []string{"name", "server", "tags"},
		},
		{
			name: "toml",
			rel:  "config.toml",
			text: "title = \"demo\"\n[server]\nport = 1\n[database.primary]\nurl = \"y\"\n",
			want: []string{"title", "server", "database.primary"},
		},
		{
			name: "ini",
			rel:  "config.ini",
			text: "[core]\nautocrlf=true\n[remote \"origin\"]\nurl=x\n",
			want: []string{"core"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractConfigKeys(tc.rel, tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("keys = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScanFillsConfigKeys(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src/main/resources/application.yml"),
		"spring:\n  redis:\n    host: localhost\nserver:\n  port: 8080\n")
	mustWrite(t, filepath.Join(dir, ".env"), "DB_HOST=localhost\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	ymlKeys := m.ConfigKeys["src/main/resources/application.yml"]
	if len(ymlKeys) == 0 {
		t.Fatalf("expected yaml keys: %+v", m.ConfigKeys)
	}
	hasSpring := false
	for _, k := range ymlKeys {
		if k == "spring.redis" {
			hasSpring = true
		}
	}
	if !hasSpring {
		t.Fatalf("spring.redis key missing: %v", ymlKeys)
	}
	envKeys := m.ConfigKeys[".env"]
	if len(envKeys) != 1 || envKeys[0] != "DB_HOST" {
		t.Fatalf("env keys wrong: %v", envKeys)
	}
}
