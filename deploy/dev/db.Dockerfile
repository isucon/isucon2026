FROM mirror.gcr.io/library/postgres:16.9-alpine@sha256:7c688148e5e156d0e86df7ba8ae5a05a2386aaec1e2ad8e6d11bdf10504b1fb7
RUN apk add --no-cache iproute2
COPY deploy/dev/routes.sh deploy/dev/db-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/db-entrypoint.sh
ENTRYPOINT ["db-entrypoint.sh"]
CMD ["postgres"]
