package gfx

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

	case "begintransaction\n":
		fallthrough
	case "2\n":
		apimethods.BeginTransaction()

	case "commit\n":
		fallthrough
	case "3\n":
		apimethods.Commit()

	case "createdocument\n":
		fallthrough
	case "4\n":
		apimethods.CreateDocument()

	case "deletedocument\n":
		fallthrough
	case "5\n":
		apimethods.DeleteDocument()

	case "getdocument\n":
		fallthrough
	case "6\n":
		apimethods.GetDocument()

	case "listcollectionids\n":
		fallthrough
	case "7\n":
		apimethods.ListCollectionIds()

	case "listdocuments\n":
		fallthrough
	case "8\n":
		apimethods.ListDocuments()

	case "rollback\n":
		fallthrough
	case "9\n":
		apimethods.Rollback()

	case "runquery\n":
		fallthrough
	case "10\n":
		apimethods.RunQuery()

	case "updatedocument\n":
		fallthrough
	case "11\n":
		apimethods.UpdateDocument()

	case "write\n":
		fallthrough
	case "12\n":
		apimethods.Write()

	case "createindex\n":
		fallthrough
	case "13\n":
		apimethods.CreateIndex()

	case "deleteindex\n":
		fallthrough
	case "14\n":
		apimethods.DeleteIndex()

	case "getindex\n":
		fallthrough
	case "15\n":
		apimethods.GetIndex()

	case "listindexes\n":
		fallthrough
	case "16\n":
		apimethods.ListIndexes()

	default:
		fmt.Println("Unknown option '", api, "'")

	}

}
