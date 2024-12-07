# isucon14

## usage

### initialize

#### from local PC

**~create a new repository~ already done**

Create a new repository from https://github.com/bgpat/isucon-boilerplate/generate.

**add members**

Invite your team members from https://github.com/dbgofy/isucon14/settings/access.
After them have confirmed, run [Update SSH Keys](https://github.com/dbgofy/isucon14/actions?query=workflow%3A%22Update+SSH+Keys%22).

#### in each competition server 

Run following commands by `root` user.

**generate and register ssh deploy key**

```bash
ssh-keygen -t rsa
cat ~/.ssh/id_rsa.pub
```

Copy the result and register as a [deploy key](https://github.com/dbgofy/isucon14/settings/keys/new).

**install**

```bash
cd /
git init
git remote add origin git@github.com:dbgofy/isucon14.git
git fetch origin master
git reset --hard FETCH_HEAD
make
```

Add deploy key and run `git push -u origin master`.

### run monitoring servers

```bash
make monitoring
```

Access to 3999 port from your browser.

### edit config file

```bash
vim /etc/nginx/nginx.conf
```

### track source files by git

```bash
git add -f *.go
```

