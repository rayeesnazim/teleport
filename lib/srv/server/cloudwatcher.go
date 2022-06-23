/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/cloudflare/cfssl/log"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/srv/db/common"
	"github.com/gravitational/trace"
)

type Watcher struct {
	// Instances can be used to consume
	Instances chan []*ec2.Instance

	fetchers []fetcher
	waitTime time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
}

func (w *Watcher) Start() {
	ticker := time.NewTicker(w.waitTime)
	for {
		for _, fetcher := range w.fetchers {
			inst, err := fetcher.GetEC2Instances(w.ctx)
			if err != nil {
				log.Error("Failed to fetch EC2 instances: ", err)
				continue
			}
			w.Instances <- inst
		}
		select {
		case <-ticker.C:
			continue
		case <-w.ctx.Done():
			return
		}
	}
}

func (w *Watcher) Stop() {
	w.cancel()
}

func NewCloudServerWatcher(ctx context.Context, matchers []services.AWSMatcher, clients common.CloudClients) (*Watcher, error) {
	cancelCtx, cancelFn := context.WithCancel(ctx)
	watcher := Watcher{
		fetchers:  []fetcher{},
		ctx:       cancelCtx,
		cancel:    cancelFn,
		waitTime:  time.Minute,
		Instances: make(chan []*ec2.Instance),
	}
	for _, matcher := range matchers {
		for _, region := range matcher.Regions {
			cl, err := clients.GetAWSEC2Client(region)
			if err != nil {
				return nil, trace.Wrap(err)
			}
			fetcher, err := newEc2InstanceFetcher(matcher, region, cl)
			if err != nil {
				return nil, trace.Wrap(err)
			}
			watcher.fetchers = append(watcher.fetchers, fetcher)
		}
	}
	return &watcher, nil
}

type fetcher interface {
	GetEC2Instances(context.Context) ([]*ec2.Instance, error)
}

type ec2InstanceFetcher struct {
	Labels types.Labels
	EC2    ec2iface.EC2API
	Region string
}

func newEc2InstanceFetcher(matcher services.AWSMatcher, region string, ec2Client ec2iface.EC2API) (*ec2InstanceFetcher, error) {
	fetcherConfig := ec2InstanceFetcher{
		EC2:    ec2Client,
		Labels: matcher.Tags,
		Region: region,
	}
	return &fetcherConfig, nil
}

func (f *ec2InstanceFetcher) GetEC2Instances(ctx context.Context) ([]*ec2.Instance, error) {
	var instances []*ec2.Instance
	err := f.EC2.DescribeInstancesPagesWithContext(ctx, &ec2.DescribeInstancesInput{},
		func(dio *ec2.DescribeInstancesOutput, b bool) bool {
			for _, res := range dio.Reservations {
				instances = append(instances, res.Instances...)
			}
			return true
		})

	if err != nil {
		return nil, trace.Wrap(err)
	}
	return filterByLabels(f.Labels, instances)
}

func ec2TagsToLabels(tags []*ec2.Tag) map[string]string {
	labels := make(map[string]string)
	for _, tag := range tags {
		key := aws.StringValue(tag.Key)
		if types.IsValidLabelKey(key) {
			labels[key] = aws.StringValue(tag.Value)
		} else {
			log.Debugf("Skipping EC2 tag %q, not a valid label key", key)
		}
	}
	return labels
}

func filterByLabels(labels types.Labels, instances []*ec2.Instance) ([]*ec2.Instance, error) {
	var result []*ec2.Instance
	for _, inst := range instances {
		instanceLabels := ec2TagsToLabels(inst.Tags)
		match, _, err := services.MatchLabels(labels, instanceLabels)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		if !match {
			continue
		}
		result = append(result, inst)
	}
	return result, nil
}
