package userutil

import (
	"fmt"
	admin "go-genproto/googleapis/firestore/admin/v1beta1"
)

//DrawIndex ... Draw a single index instance
func DrawIndex(ind admin.Index) {

	fmt.Println("Index Name: " + ind.Name + "\nIndex State: " + ind.State.String())
	for _, indFields := range ind.Fields {
		fmt.Println("   Field: " + indFields.FieldPath + "   Mode: " + indFields.Mode.String())

	}
	fmt.Print("\n")

}
