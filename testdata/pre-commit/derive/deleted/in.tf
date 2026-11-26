terraform {
  provider_meta "aws" {
    # a comment
    other   =   "x"

    // x
    user_agent = ["x"]
    module_name = "m"
    user_agent   =   [
      "a",
    ]
  }
}
user_agent = "outside"
