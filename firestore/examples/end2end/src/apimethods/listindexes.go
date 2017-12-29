package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	admin "go-genproto/googleapis/firestore/admin/v1beta1"
	"userutil"
)

//ListIndexes ... List All indexes in a Database
func ListIndexes() {
	fmt.Println("\n:: Listing All Indexes ::\n")

	client, conn := fsutils.MakeFSAdminClient()

	defer conn.Close()

	listIndexesReq := admin.ListIndexesRequest{
		Parent: "projects/firestoretestclient/databases/(default)",
	}

	resp, err := client.ListIndexes(context.Background(), &listIndexesReq)
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, ind := range resp.Indexes {
		userutil.DrawIndex(*ind)
	}

	return
}
