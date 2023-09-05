# Usage

- [AWS](#aws)
- [AWS GovCloud](#aws-govcloud)
- [Infoblox](#infoblox)
- [BlueCat](#bluecat)
- [GCP](#gcp)
- [Azure](#azure)

### Credentials for DNS providers

The _external-dns-operator_ manages external-dns deployments. It creates pods with correct credentials based on the
provider configuration in the `ExternalDNS` resource. However, it does not provision the credentials themselves, instead
it expects the credentials to be in the same namespace as the operator itself. It then copies over the credentials into
the namespace where the _external-dns_ deployments are created so that they can be mounted by the pods.

# AWS

Create a secret with the access key id and secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: aws-access-key
  namespace: #operator namespace
stringData:
  credentials: |-
    [default]
    aws_access_key_id = " <AWS_ACCESS_KEY_ID>"
    aws_secret_access_key = "<AWS_SECRET_ACCESS_KEY>"
```

Then create an `ExternalDNS` resource as follows:

```yaml
apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: aws-example
spec:
  provider:
    type: AWS
    aws:
      credentials:
        name: aws-access-key
  zones: # Replace with the desired hosted zone IDs
    - "Z3URY6TWQ91KXX"
  source:
    type: Service
    fqdnTemplate:
    - '{{.Name}}.mydomain.net'
```

Once this is created the _external-dns-operator_ will create a deployment of _external-dns_ which is configured to
manage DNS records in AWS Route53.

## AWS Assume Role

The _external-dns-operator_ supports managing records in another AWS account's hosted zone. To achieve this, you will
require an IAM Role ARN with the necessary permissions properly set up. This Role ARN should then be specified in the
`ExternalDNS` resource in the following manner:

```yaml
apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: aws-example
spec:
  provider:
    type: AWS
    aws:
      credentials:
        name: aws-access-key
      assumeRole:
        arn: arn:aws:iam::123456789012:role/role-name # Replace with the desire Role ARN
  zones: # Replace with the desired hosted zone IDs
    - "Z3URY6TWQ91KXX"
  source:
    type: Service
    fqdnTemplate:
    - '{{.Name}}.mydomain.net'
```

**Note**: Due to a limitation of the `v1beta1` API requiring the `credentials` field, OpenShift users will be required
to provide an empty (`""`) credentials field. The empty credentials will be ignored and the secret provided by
OpenShift's Cloud Credentials Operator will be used:

```yaml
apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: aws-example
spec:
  provider:
    type: AWS
    aws:
      credentials:
        name: "" # Empty Credentials
      assumeRole:
        arn: arn:aws:iam::123456789012:role/role-name # Replace with the desire Role ARN
```

# AWS GovCloud
The operator makes the assumption that `ExternalDNS` instances which target GovCloud DNS also run on the GovCloud. This is needed to detect the AWS region.   
As for the rest: the usage is exactly the same as for `AWS`.

# Infoblox

Before creating an `ExternalDNS` resource for the [Infoblox](https://www.infoblox.com/wp-content/uploads/infoblox-deployment-infoblox-rest-api.pdf)
the following information is required:

1. Grid Master Host
2. WAPI version
3. WAPI port
4. WAPI username
5. WAPI password

Create a secret with the username and password as follows:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infoblox-credentials
  namespace: #operator namespace
data:
  EXTERNAL_DNS_INFOBLOX_WAPI_USERNAME: # Base-64 encoded username
  EXTERNAL_DNS_INFOBLOX_WAPI_PASSWORD: # Base-64 encoded password
```

Then create an `ExternalDNS` resource as follows:

```yaml
apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: infoblox-example
spec:
  provider:
    type: Infoblox
    infoblox:
      credentials:
        name: infoblox-credentials
      gridHost: # the grid master host from the previous step. eg: 172.26.1.200
      wapiPort: # the WAPI port, eg: 80, 443, 8080
      wapiVersion: # the WAPI version, eg: 2.11, 2.3.1
  zones: # Replace with the desired hosted zones
    - "ZG5zLm5ldHdvcmtfdmlldyQw"
  source:
    type: Service
    fqdnTemplate:
    - '{{.Name}}.mydomain.net'
```

Once this is created the _external-dns-operator_ will create a deployment of _external-dns_ which is configured to
manage DNS records in Infoblox.

# BlueCat

The BlueCat provider requires
the [BlueCat Gateway](https://docs.bluecatnetworks.com/r/Gateway-Installation-Guide/Installing-BlueCat-Gateway/20.3.1)
and the [community workflows](https://github.com/bluecatlabs/gateway-workflows) to be installed. Once the gateway is
running note down the following details:

1. Gateway Host
2. Gateway Username(optional)
3. Gateway Password(optional)
4. Root Zone

Create a JSON file with the details:

```json
{
  "gatewayHost": "https://bluecatgw.example.com",
  "gatewayUsername": "user",
  "gatewayPassword": "pass",
  "dnsConfiguration": "Example",
  "dnsView": "Internal",
  "rootZone": "example.com",
  "skipTLSVerify": false
}
```

Then create a secret in the operator namespace with the command

```bash
kubectl create secret -n $EXTERNAL_DNS_OPERATOR_NAMESPACE generic bluecat-config --from-file ~/bluecat.json
```

For more details consult the
external-dns [documentation for BlueCat](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/bluecat.md)
.

Finally, create an `ExternalDNS` resource as shown below:

```yaml

apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: bluecat-example
spec:
  provider:
    type: BlueCat
    blueCat:
      config:
        name: bluecat-config
  zones: # Replace with the desired hosted zones
    - "78127234..."
```

# GCP

Before creating an ExternalDNS resource for GCP, the following is required:

1. create a secret with the service account credentials to be used by the operator

```yaml
  apiVersion: v1
  kind: Secret
  metadata:
    name: gcp-access-key
    namespace: #operator namespace
  data:
    gcp-credentials.json: # gcp-service-account-key-file
```

2. sample ExternalDNS CR for GCP

```yaml
apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: sample-gcp
spec:
  # DNS provider
  provider:
    type: GCP
    gcp:
      credentials:
        name: gcp-access-key
      project: gcp-devel
  zones: # Replace with the desired managed zones
    - "3651032588905568971"
  source:
    type: Service
    fqdnTemplate:
    - '{{.Name}}.mydomain.net'
```

# Azure

Before creating an ExternalDNS resource for Azure, the following is required:

1. create a secret with the service account credentials to be used by the operator

```yaml
  apiVersion: v1
  kind: Secret
  metadata:
    name: azure-config-file
    namespace: #operator namespace
  data:
    azure.json: # azure-config-file
```

The contents of `azure.json` should be similar to this:

```json
{
  "tenantId": "01234abc-de56-ff78-abc1-234567890def",
  "subscriptionId": "01234abc-de56-ff78-abc1-234567890def",
  "resourceGroup": "MyDnsResourceGroup",
  "aadClientId": "01234abc-de56-ff78-abc1-234567890def",
  "aadClientSecret": "<clientSecret>"
}
```

2. sample ExternalDNS CR for Azure

```yaml
apiVersion: externaldns.olm.openshift.io/v1beta1
kind: ExternalDNS
metadata:
  name: sample-azure
spec:
  # DNS provider
  provider:
    type: Azure
    azure:
      configFile:
        name: azure-config-file
  zones: # Replace with the desired hosted zones
    - "myzoneid"
  source:
    type: Service
    fqdnTemplate:
    - '{{.Name}}.mydomain.net'
```
