function desktopPlatform() {
  const { userAgentData, userAgent, platform, maxTouchPoints } = navigator;
  if (userAgentData?.mobile || /Android|iPad|iPhone|iPod|Mobile|CrOS/i.test(userAgent)) return '';

  const system = userAgentData?.platform || platform;
  if (/^mac/i.test(system) && maxTouchPoints < 2) return 'macos';
  if (/^win/i.test(system)) return 'windows';
  if (/^linux/i.test(system)) return 'linux';
  return '';
}

function showPlatformDownload() {
  const platform = desktopPlatform();
  if (!platform) return;

  const card = document.getElementById(`download-${platform}`);
  const packageLink = card?.querySelector('a[download]');
  if (!packageLink) return;

  const action = document.getElementById('primary-download');
  const name = card.querySelector('h3').textContent;
  if (platform === 'macos') {
    action.href = packageLink.getAttribute('href');
    action.download = packageLink.download;
    action.textContent = `下载 ${name} 版`;
    const meta = document.getElementById('download-meta');
    meta.textContent = `通用版 · ${packageLink.querySelector('small').textContent}`;
    meta.hidden = false;
  } else {
    action.href = `#${card.id}`;
    action.textContent = `查看 ${name} 版本`;
  }
  document.getElementById('all-downloads').hidden = false;
}

function showWorkflowPreviews() {
  const image = document.getElementById('workflow-image');
  const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)');
  const animatePreview = () => {
    if (reduceMotion.matches) return;
    image.animate?.(
      [{ opacity: 0.65, transform: 'translateY(3px)' }, { opacity: 1, transform: 'translateY(0)' }],
      { duration: 165, easing: 'cubic-bezier(.2, .68, .25, 1)' },
    );
  };
  for (const step of document.querySelectorAll('.steps details')) {
    step.addEventListener('toggle', () => {
      if (!step.open || image.getAttribute('src') === step.dataset.image) return;
      image.addEventListener('load', animatePreview, { once: true });
      image.alt = step.dataset.alt;
      image.src = step.dataset.image;
    });
  }
}

showPlatformDownload();
showWorkflowPreviews();
