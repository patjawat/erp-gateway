import {
  AfterViewInit,
  Directive,
  ElementRef,
  EventEmitter,
  inject,
  OnDestroy,
  Output,
} from "@angular/core";
@Directive({ selector: "[gwLoadMore]", standalone: true })
export class LoadMoreDirective implements AfterViewInit, OnDestroy {
  @Output() loadMore = new EventEmitter<void>();
  private el = inject(ElementRef<HTMLElement>);
  private observer?: IntersectionObserver;
  ngAfterViewInit(): void {
    if (typeof IntersectionObserver === "undefined") return;
    this.observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((x) => x.isIntersecting)) this.loadMore.emit();
      },
      { rootMargin: "160px" },
    );
    this.observer.observe(this.el.nativeElement);
  }
  ngOnDestroy(): void {
    this.observer?.disconnect();
  }
}
