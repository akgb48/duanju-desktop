"""Read-only ISO BMFF box/auxiliary metadata inspection (partial files allowed)."""
from pathlib import Path
import struct,sys
b=Path(sys.argv[1]).read_bytes()
containers={'moov','trak','mdia','minf','stbl','edts','mvex','moof','traf','sinf','schi'}
def walk(start,end,level=0):
 p=start
 while p+8<=min(end,len(b)):
  n=struct.unpack_from('>I',b,p)[0];kind=b[p+4:p+8].decode('latin1');h=8
  if n==1:n=struct.unpack_from('>Q',b,p+8)[0];h=16
  if n==0:n=end-p
  if n<h:raise ValueError((p,kind,n))
  extra=''
  if kind in {'stsz','stsc','stco','co64','senc','saiz','saio','tenc','schm','frma','tfhd','trun'}:extra=' '+b[p+h:min(p+n,p+h+90)].hex()
  print(' '*level+f'{kind} offset={p} size={n}'+extra)
  if kind in containers:walk(p+h,min(p+n,len(b)),level+1)
  if kind=='stsd':walk(p+h+8,min(p+n,len(b)),level+1)
  if kind in {'encv','avc1','hvc1','hev1'}:walk(p+h+78,min(p+n,len(b)),level+1)
  if kind in {'enca','mp4a'}:walk(p+h+28,min(p+n,len(b)),level+1)
  p+=n
walk(0,len(b))
