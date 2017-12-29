package gfx

import (
	"apimethods"
	"fmt"
)

//ChooseAPIMethod ... Pick a method call based on menu entry
func ChooseAPIMethod(api string) {

	switch api {

	case "batchgetdocuments":
		fallthrough
	case "1":
		apimethods.BatchGetDocuments()

	case "begintransaction":
		fallthrough
	case "2":
		apimethods.BeginTransaction()

	case "commit":
		fallthrough
	case "3":
		apimethods.Commit()

	case "createdocument":
		fallthrough
	case "4":
		apimethods.CreateDocument()

	case "deletedocument":
		fallthrough
	case "5":
		apimethods.DeleteDocument()

	case "getdocument":
		fallthrough
	case "6":
		apimethods.GetDocument()

	case "listcollectionids":
		fallthrough
	case "7":
		apimethods.ListCollectionIds()

	case "listdocuments":
		fallthrough
	case "8":
		apimethods.ListDocuments()

	case "rollback":
		fallthrough
	case "9":
		apimethods.Rollback()

	case "runquery":
		fallthrough
	case "10":
		apimethods.RunQuery()

	case "updatedocument":
		fallthrough
	case "11":
		apimethods.UpdateDocument()

	case "write":
		fallthrough
	case "12":
		apimethods.Write()

	case "createindex":
		fallthrough
	case "13":
		apimethods.CreateIndex()

	case "deleteindex":
		fallthrough
	case "14":
		apimethods.DeleteIndex()

	case "getindex":
		fallthrough
	case "15":
		apimethods.GetIndex()

	case "listindexes":
		fallthrough
	case "16":
		apimethods.ListIndexes()

	default:
		fmt.Println("Unknown option '", api, "'")

	}

}
