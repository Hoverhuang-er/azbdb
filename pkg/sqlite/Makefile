all:
	cd ../../cmd/azb-sharedlib && CGO_ENABLED=1 go generate

AZURE_ENV=\
	AZURE_STORAGE_ACCOUNT=$${AZURE_STORAGE_ACCOUNT} \
	AZURE_STORAGE_KEY=$${AZURE_STORAGE_KEY} \
	AZURE_STORAGE_BLOB_ENDPOINT=$${AZURE_STORAGE_BLOB_ENDPOINT}

run: all scratch.sqlite
	export ${AZURE_ENV} ; sqlite3 scratch.sqlite \
		-cmd '.load ../../cmd/azb-sharedlib/azb'

scratch.sqlite:
	export ${AZURE_ENV} ; sqlite3 scratch.sqlite \
		-cmd '.load ../../cmd/azb-sharedlib/azb' \
		"create virtual table if not exists mytable using azb \
		   (node_cache_entries=1000, \
		    azure_container='$$AZURE_STORAGE_CONTAINER', \
		    azure_endpoint='$$AZURE_STORAGE_BLOB_ENDPOINT', \
		    azure_prefix='mytable', \
		    columns='id PRIMARY KEY, name, email')"
