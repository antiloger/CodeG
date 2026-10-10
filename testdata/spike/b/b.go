package b

import "example.com/fixture/a"

func Make() *a.User { return a.CreateUser("b") }
