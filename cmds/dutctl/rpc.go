// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/google/uuid"
)

func (app *application) listRPC() error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.ListRequest{})

	res, err := app.rpcClient.List(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeDeviceList,
		Data: res.Msg.GetDevices(),
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "List Response",
		},
	})

	return nil
}

func (app *application) commandsRPC(device string) error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.CommandsRequest{Device: device})

	res, err := app.rpcClient.Commands(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandList,
		Data: res.Msg.GetCommands(),
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "Commands Response",
			"device": device,
		},
	})

	return nil
}

func (app *application) detailsRPC(device, command, keyword string) error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.DetailsRequest{
		Device:  device,
		Cmd:     command,
		Keyword: keyword,
	})

	res, err := app.rpcClient.Details(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandDetail,
		Data: res.Msg.GetDetails(),
		Metadata: map[string]string{
			"server":  app.serverAddr,
			"rpc":     "Details Response",
			"device":  device,
			"command": command,
			"keyword": keyword,
		},
	})

	return nil
}

// uploadFile uploads a file using the new FileTransfer protocol
func (app *application) uploadFile(stream *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse], filePath string) error {
	const chunkSize = 1024 * 1024 // 1MB

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	transferID := uuid.New().String()

	// 1. Send FileTransferStart
	startMsg := &pb.FileTransferStart{
		TransferId: transferID,
		Path:       filePath,
		TotalSize:  uint64(fileInfo.Size()),
		Direction:  "upload",
	}

	err = stream.Send(&pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferStart{
			FileTransferStart: startMsg,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to send FileTransferStart: %w", err)
	}

	log.Printf("Sent FileTransferStart: transfer_id=%s, path=%s, size=%d\n", transferID, filePath, fileInfo.Size())

	// 2. Receive FileTransferStartAck
	resp, err := stream.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive FileTransferStartAck: %w", err)
	}

	startAck := resp.GetFileTransferStartAck()
	if startAck == nil {
		return fmt.Errorf("expected FileTransferStartAck, got %T", resp.Msg)
	}

	if !startAck.Accepted {
		return fmt.Errorf("transfer rejected by agent: %s", startAck.ErrorMessage)
	}

	log.Printf("Received FileTransferStartAck: transfer_id=%s\n", startAck.TransferId)

	// 3. Send chunks
	offset := uint64(0)
	buf := make([]byte, chunkSize)
	chunksSent := 0

	for {
		n, readErr := file.Read(buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("failed to read file: %w", readErr)
		}

		if n == 0 {
			break
		}

		isLast := errors.Is(readErr, io.EOF) || offset+uint64(n) >= uint64(fileInfo.Size())

		chunk := &pb.FileTransferChunk{
			TransferId: transferID,
			Offset:     offset,
			Data:       buf[:n],
			IsLast:     isLast,
		}

		err = stream.Send(&pb.RunRequest{
			Msg: &pb.RunRequest_FileTransferChunk{
				FileTransferChunk: chunk,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to send chunk at offset %d: %w", offset, err)
		}

		log.Printf("Sent chunk: offset=%d, size=%d, is_last=%v\n", offset, n, isLast)
		chunksSent++
		offset += uint64(n)

		if isLast {
			break
		}
	}

	// 4. Collect all ChunkAcks
	for i := 0; i < chunksSent; i++ {
		resp, err := stream.Receive()
		if err != nil {
			return fmt.Errorf("failed to receive ChunkAck %d: %w", i+1, err)
		}

		ack := resp.GetFileTransferChunkAck()
		if ack == nil {
			return fmt.Errorf("expected FileTransferChunkAck, got %T", resp.Msg)
		}

		if ack.ErrorMessage != "" {
			return fmt.Errorf("chunk failed at offset %d: %s", ack.Offset, ack.ErrorMessage)
		}

		log.Printf("Received ChunkAck: offset=%d\n", ack.Offset)
	}

	// 5. Send FileTransferComplete
	completeMsg := &pb.FileTransferComplete{
		TransferId:       transferID,
		BytesTransferred: offset,
	}

	err = stream.Send(&pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferComplete{
			FileTransferComplete: completeMsg,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to send FileTransferComplete: %w", err)
	}

	log.Printf("Sent FileTransferComplete: transfer_id=%s, bytes=%d\n", transferID, offset)

	// 6. Receive FileTransferCompleteAck
	resp, err = stream.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive FileTransferCompleteAck: %w", err)
	}

	completeAck := resp.GetFileTransferCompleteAck()
	if completeAck == nil {
		return fmt.Errorf("expected FileTransferCompleteAck, got %T", resp.Msg)
	}

	if !completeAck.Success {
		return fmt.Errorf("transfer failed: %s", completeAck.ErrorMessage)
	}

	log.Printf("Upload complete: %d bytes transferred\n", offset)
	return nil
}

//nolint:funlen,cyclop,gocognit
func (app *application) runRPC(device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, numWorkers)

	stream := app.rpcClient.Run(runCtx)
	req := &pb.RunRequest{
		Msg: &pb.RunRequest_Command{
			Command: &pb.Command{
				Device:  device,
				Command: command,
				Args:    cmdArgs,
			},
		},
	}

	// Send initial command
	if err := stream.Send(req); err != nil {
		return err
	}

	// For file uploads, send file data
	if command == "push" && len(cmdArgs) > 0 {
		if err := app.uploadFile(stream, cmdArgs[0]); err != nil {
			return err
		}
	}

	// Receive responses in a goroutine
	go func() {
		for {
			resp, err := stream.Receive()
			if err != nil {
				if errors.Is(err, io.EOF) {
					errChan <- nil
					return
				}
				errChan <- err
				return
			}

			// Handle different message types
			switch msg := resp.Msg.(type) {
			case *pb.RunResponse_Print:
				output := msg.Print.GetText()
				fmt.Fprint(app.stdout, string(output))

			case *pb.RunResponse_Console:
				c := msg.Console
				if c.GetStdout() != nil {
					fmt.Fprint(app.stdout, string(c.GetStdout()))
				}
				if c.GetStderr() != nil {
					fmt.Fprint(app.stderr, string(c.GetStderr()))
				}

			case *pb.RunResponse_FileRequest:
				fileReq := msg.FileRequest
				log.Printf("Agent requesting file: %s\n", fileReq.Path)
				// TODO: Handle file requests from agent

			case *pb.RunResponse_FileTransferStart:
				startMsg := msg.FileTransferStart
				transferID := startMsg.TransferId

				// Create temp file for download
				dir := filepath.Dir(startMsg.Path)
				if dir == "" {
					dir = "."
				}
				os.MkdirAll(dir, 0755)

				tempFile, err := os.CreateTemp(dir, ".dutctl-transfer-*.tmp")
				if err != nil {
					errChan <- fmt.Errorf("failed to create temp file: %w", err)
					return
				}

				log.Printf("Starting download: transfer_id=%s, path=%s, size=%d\n", transferID, startMsg.Path, startMsg.TotalSize)

				// Send StartAck
				startAck := &pb.FileTransferStartAck{
					TransferId: transferID,
					Accepted:   true,
				}
				err = stream.Send(&pb.RunRequest{
					Msg: &pb.RunRequest_FileTransferStartAck{
						FileTransferStartAck: startAck,
					},
				})
				if err != nil {
					errChan <- fmt.Errorf("failed to send FileTransferStartAck: %w", err)
					return
				}

				// Download chunks and send acks
				offset := uint64(0)
				for {
					resp, err := stream.Receive()
					if err != nil {
						tempFile.Close()
						os.Remove(tempFile.Name())
						errChan <- fmt.Errorf("failed to receive message during download: %w", err)
						return
					}

					// Handle FileTransferChunk
					chunk := resp.GetFileTransferChunk()
					if chunk != nil {
						if chunk.Offset != offset {
							tempFile.Close()
							os.Remove(tempFile.Name())
							errChan <- fmt.Errorf("out-of-order chunk: expected %d, got %d", offset, chunk.Offset)
							return
						}

						n, err := tempFile.WriteAt(chunk.Data, int64(chunk.Offset))
						if err != nil {
							tempFile.Close()
							os.Remove(tempFile.Name())

							errAck := &pb.FileTransferChunkAck{
								TransferId:   transferID,
								Offset:       chunk.Offset,
								ErrorMessage: fmt.Sprintf("write failed: %v", err),
							}
							stream.Send(&pb.RunRequest{
								Msg: &pb.RunRequest_FileTransferChunkAck{
									FileTransferChunkAck: errAck,
								},
							})
							errChan <- fmt.Errorf("failed to write chunk: %w", err)
							return
						}

						offset += uint64(n)

						// Send ChunkAck
						ack := &pb.FileTransferChunkAck{
							TransferId: transferID,
							Offset:     offset,
						}
						err = stream.Send(&pb.RunRequest{
							Msg: &pb.RunRequest_FileTransferChunkAck{
								FileTransferChunkAck: ack,
							},
						})
						if err != nil {
							tempFile.Close()
							os.Remove(tempFile.Name())
							errChan <- fmt.Errorf("failed to send ChunkAck: %w", err)
							return
						}

						log.Printf("Received chunk: offset=%d, size=%d, is_last=%v\n", chunk.Offset, n, chunk.IsLast)

						if chunk.IsLast {
							// Proceed to receive FileTransferComplete
							break
						}
						continue
					}

					// Handle FileTransferComplete
					complete := resp.GetFileTransferComplete()
					if complete != nil {
						if offset != startMsg.TotalSize {
							tempFile.Close()
							os.Remove(tempFile.Name())

							errAck := &pb.FileTransferCompleteAck{
								TransferId:   transferID,
								Success:      false,
								ErrorMessage: fmt.Sprintf("incomplete transfer: got %d, expected %d", offset, startMsg.TotalSize),
							}
							stream.Send(&pb.RunRequest{
								Msg: &pb.RunRequest_FileTransferCompleteAck{
									FileTransferCompleteAck: errAck,
								},
							})
							errChan <- fmt.Errorf("incomplete transfer")
							return
						}

						// Close and rename temp file
						tempFile.Close()
						finalPath := startMsg.Path
						if len(cmdArgs) > 0 {
							finalPath = cmdArgs[0]
						}

						if err := os.Rename(tempFile.Name(), finalPath); err != nil {
							os.Remove(tempFile.Name())

							errAck := &pb.FileTransferCompleteAck{
								TransferId:   transferID,
								Success:      false,
								ErrorMessage: fmt.Sprintf("rename failed: %v", err),
							}
							stream.Send(&pb.RunRequest{
								Msg: &pb.RunRequest_FileTransferCompleteAck{
									FileTransferCompleteAck: errAck,
								},
							})
							errChan <- fmt.Errorf("failed to finalize transfer: %w", err)
							return
						}

						// Send success ack
						successAck := &pb.FileTransferCompleteAck{
							TransferId: transferID,
							Success:    true,
						}
						err := stream.Send(&pb.RunRequest{
							Msg: &pb.RunRequest_FileTransferCompleteAck{
								FileTransferCompleteAck: successAck,
							},
						})
						if err != nil {
							errChan <- fmt.Errorf("failed to send FileTransferCompleteAck: %w", err)
							return
						}

						log.Printf("Download complete: %d bytes\n", complete.BytesTransferred)
						errChan <- nil
						return
					}
				}

			default:
				// Continue processing other message types
			}
		}
	}()

	// Wait for workers to finish
	for i := 0; i < numWorkers; i++ {
		err := <-errChan
		if err != nil {
			cancel()
			return err
		}
	}

	return nil
}
