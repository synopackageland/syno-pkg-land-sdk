#!/usr/bin/env python3
"""Reject installation metadata and local filesystem references in full npm documents."""
import argparse
import json
from pathlib import Path
import re

def validate(value):
    if isinstance(value, dict):
        for key, child in value.items():
            if key in {'_from', '_resolved', '_where', '_args', '_requested'}:
                raise ValueError('Registry document contains installation metadata')
            validate(child)
    elif isinstance(value, list):
        for child in value:
            validate(child)
    elif isinstance(value, str):
        if re.search(r'(?:file:|link:|/Users/|/home/|/private/|/tmp/|/root/|[A-Za-z]:\\Users\\)', value):
            raise ValueError('Registry document contains a local filesystem reference')

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('documents', type=Path, nargs='+')
    args = parser.parse_args()
    for document in args.documents:
        validate(json.loads(document.read_bytes()))
    print('Full registry metadata: clean')
