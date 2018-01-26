package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"userutil"
)

//Write ... Stream writes to a document
func Write() {
	fmt.Println("\n:: Streaming Writes to a Document ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	// Get initial stream ID and stream token

	writeReq := firestore.WriteRequest{
		Database: "projects/firestoretestclient/databases/(default)",
	}
	writeClient, err := client.Write(context.Background())

	if err != nil {
		fmt.Println(err)
		return
	}

	err = writeClient.Send(&writeReq)

	if err != nil {
		fmt.Println(err)
		return
	}

	writeResp, err := writeClient.Recv()

	streamId := writeResp.GetStreamId()
	streamToken := writeResp.GetStreamToken()
	fieldPaths := []string{}
	fields := make(map[string]*firestore.Value)

	fmt.Print("Enter Document Name: ")
	docId := userutil.ReadFromConsole()

	docName := "projects/firestoretestclient/databases/(default)/documents/GrpcTestData/" + docId

	getDocRequest := firestore.GetDocumentRequest{
		Name: docName,
	}

	doc, err := client.GetDocument(context.Background(), &getDocRequest)

	fmt.Println("\n.... Streaming writes to " + docId + "\n")

	for {
		fmt.Print("Enter Field Name (blank when finished): ")
		fieldName := userutil.ReadFromConsole()

		if fieldName != "" {
			fieldPaths := append(fieldPaths, fieldName)
			fmt.Print("Enter Field Value: ")
			fieldValString := userutil.ReadFromConsole()
			fields[fieldName] = &firestore.Value{
				ValueType: &firestore.Value_StringValue{
					StringValue: fieldValString,
				},
			}
			doc.Fields = fields
			docMask := firestore.DocumentMask{
				FieldPaths: fieldPaths,
			}
			fsWrites := []*firestore.Write{}
			fsWrites = append(fsWrites, &firestore.Write{
				Operation: &firestore.Write_Update{
					Update: doc,
				},
				UpdateMask: &docMask,
			})
			writeReq = firestore.WriteRequest{
				Database:    "projects/firestoretestclient/databases/(default)",
				Writes:      fsWrites, //Write
				StreamId:    streamId,
				StreamToken: streamToken,
			}

			err = writeClient.Send(&writeReq)
			if err != nil {
				fmt.Println(err)
				return
			}
			writeResp, err = writeClient.Recv()
			if err != nil {
				fmt.Println(err)
				return
			}
			streamToken = writeResp.GetStreamToken()

		} else {
			break
		}
	} //for

	err = writeClient.CloseSend()

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("\nFinished writing stream!")

}
