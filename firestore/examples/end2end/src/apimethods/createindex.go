package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	admin "go-genproto/googleapis/firestore/admin/v1beta1"
	"userutil"
)

//CreateIndex ... Create a New Index
func CreateIndex() {
	fmt.Println("\n:: Creating a New Index ::\n")

	client, conn := fsutils.MakeFSAdminClient()

	defer conn.Close()

	indexFields := []*admin.IndexField{}

	for {
		fmt.Print("Enter Field Name (blank when finished): ")
		indFieldName := userutil.ReadFromConsole()
		if indFieldName != "" {
			fmt.Print("Enter Mode (*ASCENDING*/DESCENDING): ")
			indFieldMode := userutil.ReadFromConsole()

			indexFields = append(indexFields, &admin.IndexField{
				FieldPath: indFieldName,
				Mode:      chooseIndexMode(indFieldMode),
			})

		} else {
			break
		}
	}

	newIndex := admin.Index{
		CollectionId: "GrpcTestData",
		Fields:       indexFields,
	}

	createIndexReq := admin.CreateIndexRequest{
		Parent: "projects/firestoretestclient/databases/(default)",
		Index:  &newIndex,
	}

	_, err := client.CreateIndex(context.Background(), &createIndexReq)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Successfully created index! ")

}

func chooseIndexMode(indexMode string) admin.IndexField_Mode {
	if indexMode == "ASCENDING" || (indexMode != "ASCENDING" && indexMode != "DESCENDING") {
		return admin.IndexField_ASCENDING
	}
	return admin.IndexField_DESCENDING
}
