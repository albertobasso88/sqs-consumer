package consumer

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/client/metadata"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/mitchelldavis/go_localstack/pkg/localstack"
	"github.com/stretchr/testify/assert"
	"log"
	"os"
	"reflect"
	"testing"
	"time"
)

var LOCALSTACK *localstack.Localstack

func TestMain(t *testing.M) {
	os.Exit(InitializeLocalstack(t))
}

func InitializeLocalstack(t *testing.M) int {
	sqs, _ := localstack.NewLocalstackService("sqs")

	// Gather them all up...
	LOCALSTACK_SERVICES := &localstack.LocalstackServiceCollection{
		*sqs,
	}

	// Initialize the services
	var err error

	LOCALSTACK, err = localstack.NewLocalstack(LOCALSTACK_SERVICES)
	if err != nil {
		log.Fatal(fmt.Sprintf("Unable to create the localstack instance: %s", err))
	}
	if LOCALSTACK == nil {
		log.Fatal("LOCALSTACK was nil.")
	}
	defer LOCALSTACK.Destroy()

	return t.Run()
}

func TestNewSQSWorker(t *testing.T) {

	sqsConf := &SQSConf{
		Queue:               "queue",
		Concurrency:         2,
		MaxNumberOfMessages: 10,
		VisibilityTimeout:   30,
		WaitTimeSeconds:     20,
	}

	svc := &sqs.SQS{
		Client: &client.Client{
			Retryer:    nil,
			ClientInfo: metadata.ClientInfo{},
			Config:     aws.Config{},
			Handlers:   request.Handlers{},
		},
	}

	type args struct {
		conf *SQSConf
		svc  *sqs.SQS
	}
	tests := []struct {
		name    string
		args    args
		want    *SQS
		wantErr bool
	}{
		{
			name: "shouldCreateNewSQSConsumer",
			args: args{
				conf: sqsConf,
				svc:  svc,
			},
			want: &SQS{
				config: sqsConf,
				sqs:    svc,
			},

			wantErr: false,
		},

		{
			name: "shouldCreateNewSQSConsumerWithDefaultValues",
			args: args{
				conf: &SQSConf{
					Queue: "queue",
				},
				svc: svc,
			},
			want: &SQS{
				config: &SQSConf{
					Queue:               "queue",
					Concurrency:         DefaultConcurrency,
					MaxNumberOfMessages: DefaultMaxNumberOfMessages,
					VisibilityTimeout:   DefaultVisibilityTimeout,
					WaitTimeSeconds:     DefaultWaitTimeSeconds,
				},
				sqs: svc,
			},

			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewSQSConsumer(tt.args.conf, tt.args.svc)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSQSConsumer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewSQSConsumer() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSQS_handleMessages(t *testing.T) {

	svc := sqs.New(LOCALSTACK.CreateAWSSession())
	queueUrl, err := initStack(svc)

	if err != nil {
		t.Errorf("error during stack creation %v", err)
	}

	ctx, _ := context.WithTimeout(context.Background(), 2*time.Second)

	var actual []string

	type fields struct {
		config *SQSConf
		sqs    *sqs.SQS
	}
	type args struct {
		ctx       context.Context
		consumeFn ConsumerFn
	}
	var tests = []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "shouldHandleMessage",
			fields: fields{
				config: &SQSConf{
					Queue: *queueUrl,
				},
				sqs: svc,
			},
			args: args{
				ctx: ctx,
				consumeFn: func(data []byte) error {
					actual = append(actual, string(data))
					return nil
				},
			},
			wantErr: false,
		},
		{
			name: "shouldHandleMessageWithError",
			fields: fields{
				config: &SQSConf{
					Queue:             *queueUrl,
					VisibilityTimeout: 0,
				},
				sqs: svc,
			},
			args: args{
				ctx: ctx,
				consumeFn: func(data []byte) error {
					return fmt.Errorf("error consume for message %s", string(data))
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {

		actual = make([]string, 0)

		err := fillQueue(svc, aws.String(tt.fields.config.Queue), err)
		if err != nil {
			t.Errorf("error during queue message insertion %v", err)
		}

		t.Run(tt.name, func(t *testing.T) {

			s, _ := NewSQSConsumer(tt.fields.config, tt.fields.sqs)

			if err := s.handleMessages(tt.args.ctx, tt.args.consumeFn); err != nil {
				t.Errorf("handleMessages() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {

				message, err := tt.fields.sqs.ReceiveMessage(&sqs.ReceiveMessageInput{
					QueueUrl:            aws.String(tt.fields.config.Queue),
					MaxNumberOfMessages: aws.Int64(3),
				})

				if err != nil {
					t.Errorf("error during ReceiveMessage %v", err)
				}

				assert.NotNil(t, message)
				assert.Equal(t, len(message.Messages), 0)

				for _, msg := range actual {
					assert.Contains(t, []string{
						"msg1",
						"msg2",
						"msg3",
					}, msg)
				}

			} else {

				message, err := tt.fields.sqs.ReceiveMessage(&sqs.ReceiveMessageInput{
					MaxNumberOfMessages: aws.Int64(3),
					QueueUrl:            aws.String(tt.fields.config.Queue),
				})

				if err != nil {
					t.Errorf("error during ReceiveMessage %v", err)
				}

				assert.NotNil(t, message)
				assert.Equal(t, len(message.Messages), 3)
				assert.Equal(t, len(actual), 0)

			}

		})

	}
}

func initStack(svc *sqs.SQS) (*string, error) {

	queue, err := svc.CreateQueue(&sqs.CreateQueueInput{
		QueueName: aws.String("queue"),
	})

	if err != nil {
		return nil, err
	}

	return queue.QueueUrl, nil
}

func fillQueue(svc *sqs.SQS, queue *string, err error) error {
	batch := &sqs.SendMessageBatchInput{
		Entries: []*sqs.SendMessageBatchRequestEntry{
			{
				Id:          aws.String("msg1"),
				MessageBody: aws.String("msg1"),
			},
			{
				Id:          aws.String("msg2"),
				MessageBody: aws.String("msg2"),
			},
			{
				Id:          aws.String("msg3"),
				MessageBody: aws.String("msg3"),
			},
		},
		QueueUrl: queue,
	}

	messageBatch, err := svc.SendMessageBatch(batch)

	if messageBatch != nil && len(messageBatch.Failed) > 0 {
		return err
	}

	if err != nil {
		return err
	}
	return nil
}
