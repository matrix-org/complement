This directory contains a list of dockerfiles which can be used as-is with Complement. These images will pull source code directly from Github so you do not need a local checkout installed. You must run the `docker build` command inside this directory to pull in the config files into the build context.

```
$ docker build -t complement-dendrite -f Dendrite.Dockerfile .
```

Try it out by building them and then passing them as `COMPLEMENT_BASE_IMAGE` (no args required).

