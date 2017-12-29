package apimethods

import (
	"context"
	"environment"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
)

//BeginTransaction ... Use BeginTransaction API
func BeginTransaction() {
	fmt.Println("\n:: Starting New Transaction ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	options := firestore.TransactionOptions{}
	beginTransactionReq := firestore.BeginTransactionRequest{
		Database: "projects/firestoretestclient/databases/(default)",
		Options:  &options,
	}
	resp, err := client.BeginTransaction(context.Background(), &beginTransactionReq)

	if err != nil {
		fmt.Println(err)
		return
	}
	transactionId := resp.GetTransaction()
	fmt.Println("Successfully began new transaction", transactionId)
	environment.SetEnvironment(transactionId)

}
