snippet "show-app-inputs" {
  arguments {
    argument "name" { type = string }
    argument "port" {
      type = integer
      validation {
        min = 1024
        max = 65535
      }
    }
  }

  commands = [
    "printf 'Application: %s, port: %s\n' ${shellquote(args.name)} ${args.port}",
  ]
}
