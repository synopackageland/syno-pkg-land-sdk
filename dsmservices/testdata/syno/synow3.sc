
[SYNO.SDS.App.FileStation3.Instance_http]
title="HTTP"
dst.ports="7000/tcp"
port_forward="yes"
desc="File Station"

[dms]
title="HTTP"
title_key="firewall:firewall_service_opt_dsm_http"
dst.ports="5050/tcp"
port_forward="yes"
desc="DSM"
desc_key="firewall:firewall_service_desc_dsm_http"

[dms_https]
title="HTTPS"
title_key="firewall:firewall_service_opt_dsm_https"
dst.ports="5051/tcp"
port_forward="yes"
desc="DSM"
desc_key="firewall:firewall_service_desc_dsm_https"

[https]
title="HTTPS"
title_key="firewall:firewall_service_opt_https"
dst.ports="443/tcp"
desc="Web Station"
