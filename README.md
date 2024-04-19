# DagGit
*Work in progress.*

Git's docs on its [Internals](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects)
state:

```
Git is a content-addressable filesystem. Great. What does that mean?
```

It means you should `dagit` (rhymes with `maggot`). Also, read the docs linked above, but
again, `dagit`. It's as simple as running:

```bash
cd repo/path
dagit start
```

And then run `git` commands in another terminal.

## Install

### Homebrew

```bash
brew install jdoiro3/dagit/dagit
dagit -h
```

### Docker

```bash
docker pull jdoiro3/dagit:latest
docker run --rm -it -v ${PWD}:/path/to/repo --entrypoint /bin/sh jdoiro3/dagit
```

## Demo

![output](https://github.com/jdoiro3/DaGit/assets/57968347/dd27aba3-d0f8-4ef3-a45d-b3a6d3d47e83)

## Screenshots

<img width="1464" alt="Screenshot 2024-04-16 at 5 02 56 PM" src="https://github.com/jdoiro3/DaGit/assets/57968347/0ae1c50f-e4af-406b-9ca8-02a13a8001de">

<img width="952" alt="Screenshot 2024-04-16 at 5 06 33 PM" src="https://github.com/jdoiro3/DaGit/assets/57968347/77523d09-f5aa-40e0-a054-3edf1f45bd64">

## TODO

- [ ] Parse/unpack Git packfiles
- [ ] Write tests
