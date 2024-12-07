#!/bin/bash
if (git status --ignored --short "$1" | grep '^!! ' > /dev/null); then
	echo "# $1" >> /.gitignore
	dirs=$(echo "$1" | tr '/' ' ')
	for d in $dirs; do
		echo "$path/*" >> /.gitignore
		path="$path/$d"
		echo "!$path" >> /.gitignore
	done
	echo >> /.gitignore
	git add /.gitignore "$1"
	git commit -m 'Update gitignore' -m "$1"
fi
