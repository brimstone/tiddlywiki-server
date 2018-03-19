tiddlywiki-server
=================

Simple server in Go that supports saving a single tiddlywiki.

Usage
-----
```
docker run -d -p 5000:5000 \
       -e AUTH_USER=$USER \
       -e AUTH_PASS=password \
       brimstone/tiddlywiki-server
```
