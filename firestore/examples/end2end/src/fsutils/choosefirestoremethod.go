package fsutils

import (
	"apimethods"
	"fmt"
)

//ChooseAPIMethod ... Pick a method call based on menu entry
func ChooseAPIMethod(api string) {

	fmt.Println("Got ", api)

	switch api {

	case "batchgetdocuments\n":
		fallthrough
	case "1\n":
		apimethods.BatchGetDocuments()

	}

}
