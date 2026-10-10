package broken

import "example.com/fixture/a"

func Bad() int { return a.Missing }
