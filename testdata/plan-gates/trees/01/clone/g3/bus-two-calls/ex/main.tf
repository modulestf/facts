module "m" {
  source = "../mod"

}
module "n" {
  source   = "../mod"
  bus_name = "a:b"
}
