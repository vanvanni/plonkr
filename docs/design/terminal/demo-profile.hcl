name = "Web machine"

configuration {
  timezone = "Europe/Amsterdam"
  users    = [{ name = "app", service = true }]
}

tasks {
  task "Install Go" {
    order = 10
    lock {
      exists = ["/usr/local/go/bin/go"]
      eval   = ["/usr/local/go/bin/go version"]
    }
    script = [
      "./scripts/install-go.sh 1.26.0",
      "/usr/local/go/bin/go version",
    ]
  }

  task "Fetch application" {
    order = 20
    lock {
      exists = ["/srv/app/.git"]
    }
    script = [
      "git clone https://git.example.com/app.git /srv/app",
      "git -C /srv/app log -1 --oneline",
    ]
  }

  task "Build and start" {
    order = 30
    script = [
      "cd /srv/app && /usr/local/go/bin/go build -o /usr/local/bin/app .",
      "systemctl enable --now app",
    ]
  }
}
