name = "Arguments and explicit snippet forwarding"

arguments {
  argument "app_name" {
    type    = string
    prompt  = "Application name"
    default = "demo"

    validation {
      pattern = "^[a-z][a-z0-9-]{0,31}$"
      message = "Use 1–32 lowercase letters, digits or hyphens, starting with a letter."
    }
  }

  argument "port" {
    type    = integer
    prompt  = "Application port"
    default = 8080

    validation {
      min = 1024
      max = 65535
    }
  }
}

tasks {
  task "Show application inputs" {
    order = 10
    script = [
      run("show-app-inputs", { name = args.app_name, port = args.port }),
    ]
  }
}
