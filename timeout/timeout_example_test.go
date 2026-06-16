package timeout_test

import (
	"errors"
	"fmt"

	"github.com/KEINOS/go-timeout/timeout"
)

//nolint:err113 // allow define dynmaic error for example test
func ExamplePlaceholder() {
	errDummy := errors.New("This is a placeholder error")

	result := timeout.Placeholder(errDummy)
	fmt.Println(result)
	//
	// Output: This is a placeholder error
}
