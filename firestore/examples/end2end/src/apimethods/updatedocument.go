package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"userutil"
)

//UpdateDocument ... Update a Document in the Database
func UpdateDocument() {
	fmt.Println("\n:: Updating a Document ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	fields := make(map[string]*firestore.Value)
	fieldPaths := []string{}

	fmt.Print("Enter Document Name: ")
	docId := userutil.ReadFromConsole()

	docName := "projects/firestoretestclient/databases/(default)/documents/GrpcTestData/" + docId

	getDocRequest := firestore.GetDocumentRequest{
		Name: docName,
	}

	doc, err := client.GetDocument(context.Background(), &getDocRequest)

	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		fmt.Println("Enter Field Name (blank when finished): ")
		fieldName := userutil.ReadFromConsole()
		if fieldName != "" {
			fieldPaths = append(fieldPaths, fieldName)
			fmt.Println("Enter Field Value: ")
			fieldValString := userutil.ReadFromConsole()
			fields[fieldName] = &firestore.Value{
				ValueType: &firestore.Value_StringValue{
					StringValue: fieldValString,
				},
			}
		} else {
			break
		}
	}

	doc.Fields = fields

	docMask := firestore.DocumentMask{
		FieldPaths: fieldPaths,
	}

	updateDocRequest := firestore.UpdateDocumentRequest{
		Document:   doc,
		UpdateMask: &docMask,
	}

	resp, err := client.UpdateDocument(context.Background(), &updateDocRequest)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Successfully updated document!")

	userutil.DrawDocument(*resp)

}
