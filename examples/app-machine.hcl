name = "Prepare application machine"

configuration {
  groups = [
    { name = "app", gid = 1001 },
    { name = "reports", create = false },
  ]
  users = [
    { name = "app", service = true, groups = ["app"] },
    {
      name        = "reporter"
      service     = false
      groups      = ["reports"]
      create      = false
      public_keys = ["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC+GM0WIGi0SlsxPPcAtHYNB5cypqd7bB6ieb6JgS4Q+ example-only-replace-me"]
    },
  ]
  timezone = "America/Chicago"
}

repository "your-repo" {
  url        = "git@example.com:team/your-repo.git"
  path       = "/srv/your-repo"
  user       = "app"
  key        = "/home/operator/.ssh/your-repo"
  ref        = "main"
  known_hosts = ["example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC+GM0WIGi0SlsxPPcAtHYNB5cypqd7bB6ieb6JgS4Q+"]
}

unit "example.service" {
  source = "example.service.tmpl"
  vars = {
    user        = "app"
    group       = "app"
    working_dir = "/srv/your-repo"
    exec_start  = "/usr/local/bin/example-server"
  }
}

tasks {
  task "Prepare host" {
    order = 0
    script = [
      run("inspect-machine"),
      flag("vm", run("guest-agent")),
    ]
  }

  task "Create reports account" {
    order = 10
    setup = [group("reports"), user("reporter")]
  }

  task "Deploy application" {
    order = 20
    setup = [repo("your-repo"), unit("example.service")]
    script = [
      "echo application-checkout-ready",
      log("Application setup complete", "green"),
    ]
  }
}
