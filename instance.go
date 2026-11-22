package main

import "errors"

// errAnotherInstance is returned by lockInstance when another instance of
// the program already holds the single-instance lock.
var errAnotherInstance = errors.New("another instance is already running")

// instanceLockPath is a fixed file in the working directory. It is never
// deleted by the program; only the OS lock on it matters.
const instanceLockPath = "ambientai.lock"
