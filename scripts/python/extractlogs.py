import re

ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")

f = open("results_stress.txt", "x")

with open(".localnet/logs/publisher.log", "r") as file:
    for line in file:
        if line.__contains__("New cTx processed"):
            clean = ANSI_RE.sub("", line).strip()
            fields = dict(re.findall(r"(\w+):\s*([^,]+),?", clean))
            f.write(
                "ID:" + fields["xt_id"].strip('"') +
                " time_in_queue:" + fields["time_in_queue"] +
                " time_in_decision:" + fields["time_in_decision"] +
                " time_to_inclusion:" + fields["time_to_inclusion"] +
                " total_duration_latency_ms:" + fields["total_duration_latency_ms"] +
                "\n"
            )

f.close()