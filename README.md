# s3n

Simple S3 object navigator

# How to install

```bash

go build -o /usr/local/bin/s3n .
```


# How to use

```bash

# make sure proper AWS credentials are configured
s3n <bucket-name>

```

# Features

1. List all objects, navigate into virtual directories using `enter` and `backspace` (hit `?` for all hotkeys)
2. View object content with `ctrl+v` using `less` command
3. Edit object content with `ctrl+e` using `$EDITOR` envvar
4. Add a new object with `ctrl+a` and edit it

# How to test locally

- Start localstack from docker-compose

```
docker-compose up -d
```

- Run create some dummy files to test

```
make createall
```


- Run in local with `make localrun` and enjoy!
