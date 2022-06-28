---
authors: Alex McGrath (alex.mcgrath@goteleport.com)
state: draft
---

# RFD 57 - Automatic discovery and enrollment of AWS servers

## What

Proposes a way by which an SSH service might automatically discover and register AWS
EC2 instances.

## Why

Currently when adding a new AWS server, it's required that Teleport be installed
after the server has been provisioned which may be a slow process for organizations
with large numbers of servers as it needs to be installed and then added to the
teleport cluster

With the changes described in this document, Teleport will be able to resolve the
issues with adding AWS servers to Teleport clusters automatically.


## Discovery

Discovery can use a matcher similar to the `db_service/aws` matcher, however EC2
instances will have an optional install command:

```yaml
ssh_service:
  enabled: "yes"
  aws:
  - types: ["ec2"]
    regions: ["us-west-1"]
    tags:
      "teleport": "yes" # aws tags to match
    ssm_command_document: ssm_command_document_name
```

The agent will use EC2's `DescribeInstances` API in order to list instances[1]. This
will require the teleport SSH agent to include `ec2:DescribeInstances` as part of
it's IAM permissions

As with AWS database discover, new EC2 nodes will be discovered periodically on a 60
second timer, as new nodes are found they will be added to the teleport cluster.

In order to avoid attempting to reinstall teleport on top of an instance where it is
already present the generated teleport config will include a static label indicating
it was created via auto discovery.

Example:
```json
{
  "kind": "node",
  "version": "v2",
  "metadata": {
    "name": "AWS_INSTANCE_ID",
    "labels": {
      "env": "example"
      "teleport.dev/discovered-node": "yes"
    },
  },
  "spec": {
    "public_addr": "ec2-54-194-252-215.us-west-1.compute.amazonaws.com",
    "hostname": "awsxyz"
  }
}
```

IAM join tokens will have a `tags` field added to the accounts so so only EC2
instances with matching `tags` can be added using the token. the tags matching will
default to `"*":"*"` to retain compatibility with previously generated IAM tokens

## Agent installation

In order to install the Teleport agent on EC2 instances, Teleport will serve an
install script at `/webapi/installer`. This will be editable as a `tctl`
resource.

Example resource script:
```yaml
kind: installer
metadata:
spec:
  # shell script that will be downloaded an run by the EC2 node
  script: |
    #!/bin/sh
    curl https://.../teleport-pubkey.asc ...
    echo "deb [signed-by=... stable main" | tee ... > /dev/null
    apt-get update
    apt-get install teleport
    teleport node configure --auth-agent=...  --join-method=iam --token-name=iam-
  # Any resource in Teleport can automatically expire.
  expires: 0001-01-01T00:00:00Z
```

If a installer resource is configured to expire it will return an empty file
after expiration.

Unless overridden by a user, a default teleport installer command will be
generated that is appropriate for the current running version and operating
system initially supporting DEB and RPM based distros that Teleport already
provides packages for.

The user must create a custom SSM Command document that will be used to execute
the served command.

Example SSM aws:runCommand document:
```yaml
# name: installTeleport
---
schemaVersion: '2.2'
description: aws:runShellScript
mainSteps:
- action: aws:downloadContent
  name: downloadContent
  inputs:
    sourceType: "HTTP"
    destinationPath: "/tmp/installTeleport.sh"
    sourceInfo:
      url: "https://teleportcluster.xyz/webapi/installer.sh"
- action: aws:runShellScript
  name: runShellScript
  inputs:
    timeoutSeconds: '300'
    runCommand:
      - /bin/sh /tmp/installTeleport.sh

```

In order to run the new SSM document the AWS user will need IAM permissions to run
SSM commands[3] for example:

```json
{
    "Statement": [
        {
            "Action": "ssm:SendCommand",
            "Effect": "Allow",
            "Resource": [
                # Allow running commands on all us-west-2 instances
                "arn:aws:ssm:us-west-2:*:instance/*",
                 # Allows running the installTeleport docuemnt on the allowed instances
                "arn:aws:ssm:us-east-2:aws-account-ID:document/installTeleport"
            ]
        }
    ]
}
```

On AWS, Amazon Linux and Ubuntu LTS (16.04, 18.04, 20.04) come with the SSM agent
preinstalled[4].



## teleport.yaml generation

The `teleport node configure` subcommand will be used to generate a
new /etc/teleport.yaml file:
```
teleport node configure
    --auth-server=auth-server.example.com [auth server that is being connected to]
    --join-method=iam
    --token=iam-token-name
    --labels=teleport.dev/origin=cloud
```
This will create generate a file with the following contents:

```yaml
teleport:
  auth_servers:
    - "auth-server.example.com:3025"
  join_params:
    token_name: iam-token-name
    method: iam
ssh_service:
  enabled: yes
  labels:
    teleport.dev/origin: "cloud"
```

## UX

### User has 1 account to discover servers on

#### Teleport config

Discovery server:
```yaml
teleport:
  ...
auth_service:
  enabled: "yes"
ssh_service:
  enabled: "yes"
  aws:
  - types: ["ec2"]
    regions: ["us-west-1"]
    tags:
      "teleport": "yes" # aws tags to match
    accounts:
    - aws_account: "222222222222"
      aws_roles:
      - "arn:aws:iam::222222222222:role/teleport-DescribeInstances-role"
      - "arn:aws:iam::222222222222:role/teleport-Install-role"
```

IAM joining token
```yaml
kind: token
version: v2
metadata:
  name: iam-token
  expires: "3000-01-01T00:00:00Z"
spec:
  roles: [Node]
  join_method: iam
  allow:
  - aws_account: "222222222222"
    tags:
      "*": "*"
```

#### AWS configuration and IAM permissions

An SSM document must be created to download and run the teleport install script.
The script will be generated using a configuration appropriate for the system
running Teleport.

```yaml
# name: installTeleport
---
schemaVersion: '2.2'
description: aws:runShellScript
mainSteps:
- action: aws:downloadContent
  name: downloadContent
  inputs:
    sourceType: "HTTP"
    destinationPath: "/tmp/installTeleport.sh"
    sourceInfo:
      url: "https://teleportcluster.xyz/webapi/installer"
- action: aws:runShellScript
  name: runShellScript
  inputs:
    timeoutSeconds: '300'
    runCommand:
      - /bin/sh /tmp/installTeleport.sh

```

The discovery node should have IAM permissions to call ec2:SendCommand and then
limit it to the `installTeleport` document:

```js
{
    "Statement": [
        {
            "Action": "ssm:SendCommand",
            "Effect": "Allow",
            "Resource": [
                # Allow running commands on all instances
                "*",
                # allow running the installTeleport document
                "arn:aws:ssm:*:aws-account-ID:document/installTeleport"
            ]
        }
    ]
}
```

The SSH discovery node should have permission to call `ec2:DescribeInstances`
```js
{
    "Statement": [
        {
            "Action": [
                "ec2:DescribeInstances",
            ],
            "Effect": "Allow",
            "Resource": [
                "*", # for example, allow on all ec2 instance with SSM availablea
            ]
        }
    ]
}
```

## Security Considerations


## Future work

### Agentless installation

In addition to supporting automatic Teleport agent installation, an agentless option
will also be supported. This mode will update the OpenSSH CA to use the Teleport CA
without installing the full Teleport Agent.

This will require changes to Teleport in order to support RBAC of OpenSSH based
nodes and to support showing the nodes in tsh/web ui.

Agentless mode will be able to make use of the same script resource endpoint as is
used when the agent is being installed to perform the CA updating.

This mode can be enabled by setting `agentless: true` in the matcher. When the
matcher includes this a predefined script for agentless updating will be used for the
endpoint.

Example agentless config:

```yaml
ssh_service:
  enabled: "yes"
  aws:
  - types: ["ec2"]
    regions: ["us-west-1"]
    tags:
      "teleport": "yes" # aws tags to match
    ssm_command_document: ssm_command_document_name
    agentless: true
```

### Assume roles

In the future the option to include a list of IAM roles to assume for
different accounts may be included:

```yaml
ssh_service:
  enabled: "yes"
  aws:
  - types: ["ec2"]
    regions: ["us-west-1"]
    tags:
      "teleport": "yes"
    ssm_command_document: ssm_command_document_name
    roles: # list of ARNs for IAM roles to assume
      - "arn:aws:iam::222222222222:role/teleport-DescribeInstancesInstall-role"
```

## Refs:
[1]: https://goteleport.com/docs/setup/guides/joining-nodes-aws-iam/
[2]: https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstances.html
[3]: https://docs.aws.amazon.com/systems-manager/latest/userguide/security_iam_id-based-policy-examples.html
[4]: https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-restrict-command-access.html
[5]: https://docs.aws.amazon.com/systems-manager/latest/userguide/ssm-agent.html
