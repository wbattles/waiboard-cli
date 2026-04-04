# waiboard-cli

command line tool for [waiboard](https://github.com/wbattles/waiboard).

## install

```
git clone https://github.com/wbattles/waiboard-cli.git
cd waiboard-cli
go build -o waiboard .
```

move the `waiboard` binary somewhere in your path:

```
sudo mv waiboard /usr/local/bin/
```

## setup

1. log in to your waiboard instance
2. go to settings and generate an api key
3. connect the cli:

```
waiboard login --url http://localhost:8000 --user admin --key your-api-key
```

the url can be a domain or ip. credentials are saved to `~/.waiboard`.

## commands

```
waiboard projects                              # list your projects
waiboard tickets                               # all tickets
waiboard tickets -p TST                        # filter by project code
waiboard tickets -s todo                       # filter by status
waiboard tickets -p TST -s inprogress          # both
waiboard tickets -m                            # only assigned to you
waiboard ticket TST-1                          # ticket detail
waiboard move TST-1 inprogress                 # change ticket status
waiboard new -p TST "fix the login bug"        # create a ticket
waiboard new -p TST -d "details here" "title"  # create with description
waiboard whoami                                # show current user
waiboard logout                                # clear saved credentials
```

tickets display as `TST-1`, `TST-2`, etc. — each project starts numbering at 1. you can use `TST-1` or the db id `1` anywhere a ticket id is needed.

## statuses

`todo`, `inprogress`, `testing`, `done`
