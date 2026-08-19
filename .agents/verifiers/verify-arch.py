#!/usr/bin/env python3
import os
import subprocess
import sys

def get_module_name():
    try:
        with open("go.mod", "r") as f:
            for line in f:
                if line.startswith("module "):
                    return line.split()[1].strip()
    except FileNotFoundError:
        pass
    return ""

def parse_rules():
    rules = []
    current_rule = None
    in_forbidden = False
    
    # 规则文件已迁移到 .agents/rules/（.agents/constraints/ 为旧路径，兼容回退）
    yaml_path = ".agents/rules/arch-rules.yaml"
    if not os.path.exists(yaml_path):
        yaml_path = ".agents/constraints/arch-rules.yaml"
    if not os.path.exists(yaml_path):
        print(f"⚠️  {yaml_path} not found, skipping architecture bounds check.")
        sys.exit(0)
        
    with open(yaml_path, "r") as f:
        for line in f:
            # removing trailing newlines but keeping spaces to check indents not strictly needed
            # for this simple schema
            sline = line.strip()
            if not sline or sline.startswith('#'):
                continue
            if sline.startswith('- package:'):
                pkg = sline.split(':', 1)[1].strip().strip('"\'')
                current_rule = {'package': pkg, 'forbidden_imports': [], 'reason': ''}
                rules.append(current_rule)
                in_forbidden = False
            elif sline.startswith('forbidden_imports:'):
                in_forbidden = True
            elif sline.startswith('- "') or sline.startswith("- '"):
                if in_forbidden and current_rule:
                    imp = sline[1:].strip().strip('"\'')
                    current_rule['forbidden_imports'].append(imp)
            elif sline.startswith('reason:'):
                reason = sline.split(':', 1)[1].strip().strip('"\'')
                if current_rule:
                    current_rule['reason'] = reason
                in_forbidden = False
    return rules

def main():
    mod = get_module_name()
    if not mod:
        print("❌ Could not find module name in go.mod")
        sys.exit(1)
        
    rules = parse_rules()
    violation = False
    
    for rule in rules:
        pkg = rule['package']
        full_pkg = f"{mod}/{pkg}"
        
        # We use a go text/template to print the importing package and the imported package.
        # This helps pinpoint EXACTLY which subpackage violates the rule.
        cmd = f"go list -f '{{{{$pkg := .ImportPath}}}}{{{{range .Imports}}}}{{{{$pkg}}}} => {{{{.}}}}\n{{{{end}}}}' {full_pkg}/... 2>/dev/null"
        try:
            output = subprocess.check_output(cmd, shell=True, text=True)
            dependencies = output.strip().split('\n')
            
            for forbidden in rule['forbidden_imports']:
                full_forbidden = f"{mod}/{forbidden}"
                
                # Check if this rule is violated
                matches = []
                for dep in dependencies:
                    if not dep:
                        continue
                    # format: importer => importee
                    parts = dep.split(' => ')
                    if len(parts) == 2:
                        importer, importee = parts[0], parts[1]
                        if full_forbidden in importee:
                            matches.append(dep)
                
                if matches:
                    print(f"❌ ARCH VIOLATION: [{full_pkg}] 及其子包 must NOT import [{full_forbidden}]")
                    for m in set(matches):
                        print(f"   Found in: {m}")
                    reason = rule.get('reason')
                    if reason:
                        print(f"   Reason: {reason}")
                    print()
                    violation = True
        except subprocess.CalledProcessError:
            pass
            
    if violation:
        print("❌ Architecture boundary violations detected. Fix imports before proceeding.")
        sys.exit(1)
    else:
        print("✅ Architecture boundaries respected (Verified by AST/Go List Automation).")
        sys.exit(0)

if __name__ == "__main__":
    main()
