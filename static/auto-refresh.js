(function() {
    // Extract project prefix from URL: /p/<slug>
    var match = location.pathname.match(/^(\/p\/[^/]+)/);
    if (!match) return;
    var hashUrl = match[1] + '/hash';
    var lastHash = null;
    var interval = 3000;

    function poll() {
        fetch(hashUrl).then(function(res) {
            return res.json();
        }).then(function(data) {
            if (lastHash === null) {
                lastHash = data.hash;
            } else if (data.hash !== lastHash) {
                location.reload();
            }
        }).catch(function() {});
    }

    setInterval(poll, interval);
    poll();
})();
