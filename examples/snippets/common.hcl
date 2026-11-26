snippet "inspect-machine" {
  commands = [
    "uname -a",
    flag("verbose", "id"),
  ]
}

snippet "guest-agent" {
  commands = ["echo guest-agent-ready"]
}
