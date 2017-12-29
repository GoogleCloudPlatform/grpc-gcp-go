package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	admin "go-genproto/googleapis/firestore/admin/v1beta1"
	"userutil"
)

//GetIndex ... Get a Specific Index
func GetIndex() {
	fmt.Println("\n:: Getting a Specfic Index ::]\n")

	client, conn := fsutils.MakeFSAdminClient()

	defer conn.Close()

	fmt.Print("Enter Index Name: ")
	indexName := "projects/firestoretestclient/databases/(default)/indexes/" + userutil.ReadFromConsole()

	getIndexReq := admin.GetIndexRequest{
		Name: indexName,
	}

	resp, err := client.GetIndex(context.Background(), &getIndexReq)

	if err != nil {
		fmt.Println(err)
		return
	}

	userutil.DrawIndex(*resp)

	fmt.Println("\nFinished getting index!\n")

}
