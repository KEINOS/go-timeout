package main

import (
	"errors"
	"fmt"
)

//nolint:err113 // allow dynamic error due to the example
func Example_wrapError() {
	var myError error

	fmt.Println("no error:", wrapError(myError, "no error"))

	myError = errors.New("original error")

	fmt.Println("is error:", wrapError(myError, "wrapped error").Error())
	//
	// Output:
	// no error: <nil>
	// is error: wrapped error: original error
}
