#!/usr/bin/env bash

ID=`uuid`
NAME=dc-${ID}
DC_DIR=tests/${NAME}

mkdir -p ${DC_DIR}

cat << EOF >> ${DC_DIR}/discovery-client.yaml
clientConfigDir: /etc/discovery-client/discovery.d/
cores:
- 0
debug:
  enablepprof: true
  endpoint: '[::]:6060'
  metrics: true
internalDir: /etc/discovery-client/internal/
logPagePaginationEnabled: false
logging:
  filename: /var/log/discovery-client.log
  level: debug
  maxAge: 96h
  maxSize: 100
  reportCaller: true
maxIOQueues: 0
reconnectInterval: 5s
EOF

DC_DISCOVERY_DIR=${DC_DIR}/discovery.d
mkdir -p ${DC_DISCOVERY_DIR}

cat << EOF >> ${DC_DISCOVERY_DIR}/ds-info
-t tcp -a 10.23.35.4 -s 8009 -q nqn.2014-08.org.nvmexpress:uuid:${ID} -n nqn.2016-01.com.lightbitslabs:uuid:0a4bd5c0-cad4-4b50-a6eb-fe1000fb1ebc:suffix
#-t tcp -a 10.23.35.5 -s 8009 -q nqn.2014-08.org.nvmexpress:uuid:${ID} -n nqn.2016-01.com.lightbitslabs:uuid:0a4bd5c0-cad4-4b50-a6eb-fe1000fb1ebc:suffix
#-t tcp -a 10.23.35.7 -s 8009 -q nqn.2014-08.org.nvmexpress:uuid:${ID} -n nqn.2016-01.com.lightbitslabs:uuid:0a4bd5c0-cad4-4b50-a6eb-fe1000fb1ebc:suffix
EOF

docker run -it --rm -d \
	--privileged \
	-P \
	-v `pwd`/${DC_DIR}:/etc/discovery-client \
	--name=${NAME} \
	lbdocker:5000/lb-nvme-discovery-client:dev
