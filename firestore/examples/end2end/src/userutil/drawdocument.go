package userutil

import (
	"fmt"

	firestore "go-genproto/googleapis/firestore/v1beta1"
)

//DrawDocument ... Draw Document Name and Fields
func DrawDocument(doc firestore.Document) {

	fmt.Println("\n\nDocument Name:", doc.Name)
	fmt.Println("  Fields: ")
	for field, value := range doc.Fields {
		fmt.Printf("   %v : %v\n", field, value.GetStringValue())
	}

}
