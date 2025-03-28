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

Then, run `git` commands in another terminal. Here's an example:

![dagit-demo](https://github.com/user-attachments/assets/e9572447-4c83-4f4d-8980-e6eba598e825)

Below is the Git object graph for `dagit`. Much wow.

<img width="1418" alt="dagit-git-dag" src="https://github.com/user-attachments/assets/04911bc9-c17a-4f27-b214-4ade61c6a778" />

## Installation

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
