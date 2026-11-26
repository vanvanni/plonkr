name = "Minimal machine check"

configuration {
  timezone = "UTC"
}

tasks {
  task "Inspect host" {
    order = 10
    script = [
      "uname -a",
      "id",
    ]
  }
}
