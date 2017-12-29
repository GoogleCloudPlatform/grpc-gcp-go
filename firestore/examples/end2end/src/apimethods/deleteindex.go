package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	admin "go-genproto/googleapis/firestore/admin/v1beta1"
	"userutil"
)

//DeleteIndex ... Delete an Index from the database
func DeleteIndex() {
	fmt.Println("\n:: Deleting an Index ::\n")

	client, conn := fsutils.MakeFSAdminClient()

	defer conn.Close()

	fmt.Print("Enter Index Name: ")
	indexName := "projects/firestoretestclient/databases/(default)/indexes/" + userutil.ReadFromConsole()

	delIndexReq := admin.DeleteIndexRequest{
		Name: indexName,
	}

	_, err := client.DeleteIndex(context.Background(), &delIndexReq)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("\nFinished deleting index!\n")

}
