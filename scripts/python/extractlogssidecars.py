import re

ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")

f = open("resultssidecars_stress.txt", "a")

filename = "sidecar-b.log"

with open(".localnet/logs/"+filename, "r") as file:
    f.write(filename+"\n")
    for line in file:
        if line.__contains__("Finished simulating cTx:"):
            clean = ANSI_RE.sub("", line).strip()
            fields = dict(re.findall(r"(\w+):\s*([^,]+),?", clean))
            f.write(
                "ID:" + fields["instance_id"].strip('"') +
                " simulation_duration_ms:" + fields["simulation_duration_ms"] +
                "\n"
            )

f.close()