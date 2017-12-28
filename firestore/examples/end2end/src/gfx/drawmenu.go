package gfx

import (
	"bufio"
	"fmt"
	"os"
)

// DrawMenu ... Draw the system menu
func DrawMenu() {

	fmt.Println("\n     Google Firestore RPC Menu")
	fmt.Println("1|batchgetdocuments ......... BatchGetDocuments")
	fmt.Println("2|begintransaction  ......... BeginTransaction")
	fmt.Println("3|commit .................... Commit")
	fmt.Println("4|createdocument ............ CreateDocument")
	fmt.Println("5|deletedocument ............ DeleteDocument")
	fmt.Println("6|getdocument ............... GetDocument")
	fmt.Println("7|listcollectionids ......... ListCollectionIds")
	fmt.Println("8|listdocuments ............. ListDocuments")
	fmt.Println("9|rollback .................. Rollback")
	fmt.Println("10|runquery ................. RunQuery")
	fmt.Println("11|updatedocument ........... UpdateDocument")
	fmt.Println("12|write .................... Write")
	fmt.Println("     Firestore Admin RPC's         ")
	fmt.Println("13|createindex .............. CreateIndex")
	fmt.Println("14|deleteindex .............. DeleteIndex")
	fmt.Println("15|getindex ................. GetIndex")
	fmt.Println("16|listindexes .............. ListIndex")
	fmt.Print("\n\nEnter an option ('quit' to exit):")

	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')

	if err != nil {
		fmt.Println(err)
	} else if text == "quit\n" {
		os.Exit(0)
	} else {
		ChooseAPIMethod(text)
	}
}
