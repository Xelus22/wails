package main

// Dummy is a second service used to check multiple-service catalogs.
type Dummy struct{}

func (d *Dummy) Ping() string { return "pong" }
