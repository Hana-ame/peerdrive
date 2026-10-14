#!/usr/bin/env python3
import re

with open('back/internal/serverapp/equiv_test.go', 'r') as f:
    content = f.read()

# Find the goldenRouteTable constant
pattern = r'(const goldenRouteTable = `122\n)(.*?)(`)'
match = re.search(pattern, content, re.DOTALL)
if match:
    header = match.group(1)
    routes = match.group(2)
    footer = match.group(3)
    
    # Routes that should NOT have /api/v1 prefix (without method)
    exclude_paths = ['/ping', '/health', '/ready', '/panel', '/panel/', '/peerjs.min.js', '/swagger/*any']
    
    new_routes = []
    for line in routes.strip().split('\n'):
        # Extract the path (second part after method)
        parts = line.split(' ', 2)
        if len(parts) < 2:
            new_routes.append(line)
            continue
        
        method = parts[0]
        path = parts[1]
        
        # Check if this path should be excluded
        should_exclude = path in exclude_paths or path.startswith('/swagger')
        
        if should_exclude:
            new_routes.append(line)
        else:
            # Add /api/v1 prefix
            parts[1] = '/api/v1' + path
            new_routes.append(' '.join(parts))
    
    new_content = content[:match.start()] + header + '\n'.join(new_routes) + '\n' + footer + content[match.end():]
    with open('back/internal/serverapp/equiv_test.go', 'w') as f:
        f.write(new_content)
    print('Updated goldenRouteTable')
else:
    print('Pattern not found')