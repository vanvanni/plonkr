package profile

import (
	"strings"
	"testing"
)

// Profiles saved on a Windows controller use CRLF. The remote shell must never
// see a carriage return, which turns `cd /srv` into `cd /srv\r`.
func TestWindowsLineEndingsNeverReachRemoteShell(t *testing.T) {
	base := t.TempDir()
	toCRLF := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	writeFixture(t, base, "windows.hcl", toCRLF(`
unit "app.service" {
  source = "app.service.tmpl"
  vars   = { name = "demo" }
}

tasks {
  task "Heredoc" {
    order  = 1
    script = [
      <<-EOT
      cd /srv
      echo done
      EOT
      ,
      run("shared"),
    ]
  }
}
`))
	writeFixture(t, base, "snippets/shared.hcl", toCRLF(`
snippet "shared" {
  commands = [
    <<-EOT
    echo shared
    EOT
  ]
}
`))
	writeFixture(t, base, "files/app.service.tmpl", toCRLF("[Unit]\nDescription=${name}\n"))

	plan, err := LoadProfile("windows", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan.Tasks {
		for _, step := range task.Steps {
			if strings.ContainsRune(step.Text, '\r') {
				t.Errorf("step %q contains a carriage return", step.Text)
			}
		}
	}
	if got := plan.Tasks[0].Steps[0].Text; got != "cd /srv\necho done\n" {
		t.Errorf("heredoc command = %q", got)
	}
	if got := plan.Units["app.service"].Content; got != "[Unit]\nDescription=demo\n" {
		t.Errorf("unit content = %q", got)
	}
}
