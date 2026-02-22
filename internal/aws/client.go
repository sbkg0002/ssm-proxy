package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Client wraps AWS SDK clients for EC2 and SSM
type Client struct {
	cfg       aws.Config
	ec2Client *ec2.Client
	ssmClient *ssm.Client
	region    string
}

// Instance represents an EC2 instance with relevant details
type Instance struct {
	InstanceID       string
	Name             string
	State            string
	InstanceType     string
	PrivateIP        string
	PublicIP         string
	AvailabilityZone string
	SSMConnected     bool
	Tags             map[string]string
}

// NewClient creates a new AWS client with the specified profile and region
func NewClient(ctx context.Context, profile, region string) (*Client, error) {
	var opts []func(*config.LoadOptions) error

	// Set profile if specified
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}

	// Set region if specified
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %w", err)
	}

	// Get actual region being used
	actualRegion := cfg.Region
	if actualRegion == "" {
		actualRegion = "us-east-1" // Default fallback
	}

	return &Client{
		cfg:       cfg,
		ec2Client: ec2.NewFromConfig(cfg),
		ssmClient: ssm.NewFromConfig(cfg),
		region:    actualRegion,
	}, nil
}

// GetInstance retrieves details for a specific EC2 instance by ID
func (c *Client) GetInstance(ctx context.Context, instanceID string) (*Instance, error) {
	input := &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}

	result, err := c.ec2Client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe instance: %w", err)
	}

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	ec2Instance := result.Reservations[0].Instances[0]
	instance := c.convertEC2Instance(ec2Instance)

	// Check SSM connectivity
	ssmConnected, err := c.isSSMConnected(ctx, instanceID)
	if err != nil {
		// Log warning but don't fail
		ssmConnected = false
	}
	instance.SSMConnected = ssmConnected

	return instance, nil
}

// FindInstancesByTag finds EC2 instances matching the specified tag
func (c *Client) FindInstancesByTag(ctx context.Context, key, value string) ([]*Instance, error) {
	input := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String(fmt.Sprintf("tag:%s", key)),
				Values: []string{value},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	}

	result, err := c.ec2Client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe instances: %w", err)
	}

	var instances []*Instance
	for _, reservation := range result.Reservations {
		for _, ec2Instance := range reservation.Instances {
			instance := c.convertEC2Instance(ec2Instance)

			// Check SSM connectivity
			ssmConnected, err := c.isSSMConnected(ctx, instance.InstanceID)
			if err != nil {
				ssmConnected = false
			}
			instance.SSMConnected = ssmConnected

			instances = append(instances, instance)
		}
	}

	return instances, nil
}

// ListInstances lists all running EC2 instances
func (c *Client) ListInstances(ctx context.Context, ssmOnly bool) ([]*Instance, error) {
	filters := []ec2types.Filter{
		{
			Name:   aws.String("instance-state-name"),
			Values: []string{"running"},
		},
	}

	input := &ec2.DescribeInstancesInput{
		Filters: filters,
	}

	result, err := c.ec2Client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe instances: %w", err)
	}

	var instances []*Instance
	for _, reservation := range result.Reservations {
		for _, ec2Instance := range reservation.Instances {
			instance := c.convertEC2Instance(ec2Instance)

			// Check SSM connectivity
			ssmConnected, err := c.isSSMConnected(ctx, instance.InstanceID)
			if err != nil {
				ssmConnected = false
			}
			instance.SSMConnected = ssmConnected

			// Filter by SSM connectivity if requested
			if ssmOnly && !ssmConnected {
				continue
			}

			instances = append(instances, instance)
		}
	}

	return instances, nil
}

// isSSMConnected checks if the SSM agent is connected for the given instance
func (c *Client) isSSMConnected(ctx context.Context, instanceID string) (bool, error) {
	input := &ssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{
			{
				Key:    aws.String("InstanceIds"),
				Values: []string{instanceID},
			},
		},
	}

	result, err := c.ssmClient.DescribeInstanceInformation(ctx, input)
	if err != nil {
		return false, err
	}

	if len(result.InstanceInformationList) == 0 {
		return false, nil
	}

	// Check if ping status is online
	info := result.InstanceInformationList[0]
	return info.PingStatus == ssmtypes.PingStatusOnline, nil
}

// convertEC2Instance converts an EC2 SDK instance to our Instance type
func (c *Client) convertEC2Instance(ec2Instance ec2types.Instance) *Instance {
	instance := &Instance{
		InstanceID:       aws.ToString(ec2Instance.InstanceId),
		State:            string(ec2Instance.State.Name),
		InstanceType:     string(ec2Instance.InstanceType),
		PrivateIP:        aws.ToString(ec2Instance.PrivateIpAddress),
		PublicIP:         aws.ToString(ec2Instance.PublicIpAddress),
		AvailabilityZone: aws.ToString(ec2Instance.Placement.AvailabilityZone),
		Tags:             make(map[string]string),
	}

	// Extract tags
	for _, tag := range ec2Instance.Tags {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		instance.Tags[key] = value

		// Set Name if present
		if key == "Name" {
			instance.Name = value
		}
	}

	// Use instance ID as name if no Name tag
	if instance.Name == "" {
		instance.Name = instance.InstanceID
	}

	return instance
}

// Config returns the underlying AWS config
func (c *Client) Config() aws.Config {
	return c.cfg
}

// Region returns the AWS region being used
func (c *Client) Region() string {
	return c.region
}

// EC2Client returns the underlying EC2 client
func (c *Client) EC2Client() *ec2.Client {
	return c.ec2Client
}

// SSMClient returns the underlying SSM client
func (c *Client) SSMClient() *ssm.Client {
	return c.ssmClient
}
